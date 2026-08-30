// Copyright 2026 The Kubernetes Network Plumbing Working Group authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package neutrondhcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/containernetworking/cni/pkg/skel"
	cnitypes "github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	k8sop "github.com/k8snetworkplumbingwg/ovs-cni/pkg/k8s-op"
	"github.com/k8snetworkplumbingwg/ovs-cni/pkg/types"
	"github.com/k8snetworkplumbingwg/ovs-cni/pkg/utils" // or wherever logutils is
)

const PluginName = "neutron-dhcp"

var (
	NeutronIpAddrGvr = schema.GroupVersionResource{
		Group:    "openstack.humanz.moe",
		Version:  "v1",
		Resource: "neutronipaddresses",
	}
)

func loadNetConf(stdinData []byte, envArgs string) (*types.NetConf, error) {
	if len(stdinData) == 0 {
		return nil, fmt.Errorf("missing CNI stdin data")
	}

	var netconf types.NetConf
	if err := json.Unmarshal(stdinData, &netconf); err != nil {
		return nil, fmt.Errorf("failed to parse CNI config: %w", err)
	}

	args := types.EnvArgs{}
	if err := cnitypes.LoadArgs(envArgs, &args); err != nil {
		return nil, fmt.Errorf("LoadArgs - CNI Args Parsing Error: %s", err)
	}

	netconf.IPAM.PodName = string(args.K8S_POD_NAME)
	netconf.IPAM.PodNamespace = string(args.K8S_POD_NAMESPACE)
	netconf.IPAM.PodUID = string(args.K8S_POD_UID)
	return &netconf, nil
}

func CmdAdd(args *skel.CmdArgs) error {
	netconf, err := loadNetConf(args.StdinData, args.Args)
	if err != nil {
		return err
	}

	err = Add(netconf, args)
	return err
}

func CmdCheck(args *skel.CmdArgs) error {
	netconf, err := loadNetConf(args.StdinData, args.Args)
	if err != nil {
		return err
	}
	return Check(netconf, args)
}

func CmdDel(args *skel.CmdArgs) error {
	netconf, err := loadNetConf(args.StdinData, args.Args)
	if err != nil {
		return err
	}
	return Del(netconf, args)
}

func Add(netconf *types.NetConf, args *skel.CmdArgs) error {
	if netconf.IPAM.Type != PluginName {
		return fmt.Errorf("unexpected plugin type %q", netconf.IPAM.Type)
	}

	k8sClient, err := k8sop.Initk8sClient(netconf)
	if err != nil {
		return fmt.Errorf("failed to initialize k8s client: %v", err)
	}

	listOptions := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("state=resv-%s,network=%s", netconf.IPAM.PodUID, netconf.IPAM.NeutronUUID),
	}

	ipList, err := k8sClient.Resource(NeutronIpAddrGvr).Namespace(netconf.IPAM.PodNamespace).List(context.TODO(), listOptions)
	if err != nil {
		return fmt.Errorf("failed to list NeutronIPAddresses: %v", err)
	}

	if len(ipList.Items) == 0 {
		return fmt.Errorf("no reservation IP addresses for Pod UID %s and Neutron UUID %s", netconf.IPAM.PodUID, netconf.IPAM.NeutronUUID)
	}

	result := &current.Result{}
	item := ipList.Items[0]

	ipaddr, found, err := unstructured.NestedString(item.Object, "spec", "ipAddress")
	if err != nil {
		return fmt.Errorf("Error unstructured resources: %v", err)
	}

	if !found {
		return fmt.Errorf("neutron address:%s Ip Address not found", item.GetName())

	}

	subnet, found, err := unstructured.NestedString(item.Object, "spec", "subnet")
	if err != nil {
		return fmt.Errorf("Error unstructured resources: %v", err)
	}

	if !found {
		return fmt.Errorf("neutron address:%s Subnet not found", item.GetName())
	}

	gw := net.IP{}
	if netconf.IPAM.UseGateway {
		gateway, found, err := unstructured.NestedString(item.Object, "metadata", "labels", "gateway")
		if err != nil {
			return fmt.Errorf("Error unstructured resources: %v", err)
		}

		if !found {
			return fmt.Errorf("neutron address:%s Gateway not found", item.GetName())
		}

		if gateway == "None" {
			return fmt.Errorf("neutron subnet:%s Gateway is None", subnet)
		}

		gw = utils.ParseIP(gateway, subnet).IP
	}

	result.IPs = append(result.IPs, &current.IPConfig{
		Address: utils.ParseIP(ipaddr, subnet),
		Gateway: gw,
	})

	if netconf.IPAM.Routes != nil {
		result.Routes = netconf.IPAM.Routes
	}

	// Update the IP address state to "bound" after it is assigned to the container
	err = unstructured.SetNestedField(item.Object, "bound", "metadata", "labels", "state")
	if err != nil {
		return fmt.Errorf("Error update unstructured label: %v", err)
	}

	err = unstructured.SetNestedField(item.Object, args.IfName, "metadata", "labels", "iface")
	if err != nil {
		return fmt.Errorf("Error update unstructured label: %v", err)
	}

	itemAnnotations := item.GetAnnotations()
	nConf := itemAnnotations["openstack.humanz.moe/neutronConfig"]
	itemAnnotations = map[string]string{
		"openstack.humanz.moe/neutronConfig": nConf,
		"openstack.humanz.moe/podName":       netconf.IPAM.PodName,
		"openstack.humanz.moe/podNamespace":  netconf.IPAM.PodNamespace,
		"openstack.humanz.moe/podUID":        netconf.IPAM.PodUID,
	}
	item.SetAnnotations(itemAnnotations)

	_, err = k8sClient.Resource(NeutronIpAddrGvr).Namespace(netconf.IPAM.PodNamespace).Update(context.Background(), &item, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("Error update resources label: %v", err)
	}

	pod, err := k8sop.GetPodsResource(k8sClient, netconf.IPAM.PodNamespace, netconf.IPAM.PodName)
	if err != nil {
		return fmt.Errorf("failed to get pod: %v", err)
	}

	pod.SetAnnotations(map[string]string{
		"openstack.humanz.moe/neutronipaddresses": item.GetName(),
	})

	err = k8sop.UpdatePodResource(k8sClient, pod)
	if err != nil {
		return fmt.Errorf("failed to update pod annotations: %v", err)
	}

	return cnitypes.PrintResult(result, netconf.CNIVersion)
}

func Check(netconf *types.NetConf, args *skel.CmdArgs) error {
	if netconf.IPAM.Type != PluginName {
		return fmt.Errorf("unexpected plugin type %q", netconf.IPAM.Type)
	}
	return fmt.Errorf("%s IPAM CHECK is not implemented", PluginName)
}

func Del(netconf *types.NetConf, args *skel.CmdArgs) error {
	if netconf.IPAM.Type != PluginName {
		return fmt.Errorf("unexpected plugin type %q", netconf.IPAM.Type)
	}

	k8sClient, err := k8sop.Initk8sClient(netconf)
	if err != nil {
		return fmt.Errorf("failed to initialize k8s client: %v", err)
	}

	listOptions := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("state=bound,network=%s", netconf.IPAM.NeutronUUID),
	}

	ipList, err := k8sClient.Resource(NeutronIpAddrGvr).Namespace(netconf.IPAM.PodNamespace).List(context.TODO(), listOptions)
	if err != nil {
		return fmt.Errorf("failed to list NeutronIPAddresses: %v", err)
	}

	for _, item := range ipList.Items {
		if item.GetAnnotations()["openstack.humanz.moe/podUID"] == netconf.IPAM.PodUID {

			// Update the IP address state to "unbound" after it is released from the container
			err = unstructured.SetNestedField(item.Object, "unbound", "metadata", "labels", "state")
			if err != nil {
				return fmt.Errorf("Error update unstructured label: %v", err)
			}

			naddrAnnotations := item.GetAnnotations()
			nConf := naddrAnnotations["openstack.humanz.moe/neutronConfig"]
			delete(naddrAnnotations, "openstack.humanz.moe/podName")
			delete(naddrAnnotations, "openstack.humanz.moe/podNamespace")
			delete(naddrAnnotations, "openstack.humanz.moe/podUID")
			item.SetAnnotations(map[string]string{
				"openstack.humanz.moe/neutronConfig": nConf,
			})

			_, err = k8sClient.Resource(NeutronIpAddrGvr).Namespace(netconf.IPAM.PodNamespace).Update(context.Background(), &item, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("Error update resources label: %v", err)
			}

			break
		}
	}

	return nil
}
