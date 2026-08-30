package k8sop

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/k8snetworkplumbingwg/ovs-cni/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	PodGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
)

// Initk8sClient initializes a Kubernetes client and retrieves the NeutronConfig resource to populate the NeutronUUID in the NetConf.
func Initk8sClient(netconf *types.NetConf) (*dynamic.DynamicClient, error) {
	kubeconfigFlag := flag.String("kubeconfig", netconf.IPAM.KubeConfig, "Path to the kubeconfig file")
	flag.Parse()
	// Load kubeconfig and create K8s client
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfigFlag)
	if err != nil {
		return nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
	}

	// The .Resource() method returns a NamespaceableResourceInterface
	nConfig, err := dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "openstack.humanz.moe",
		Version:  "v1",
		Resource: "neutronconfigs",
	}).Namespace(netconf.IPAM.PodNamespace).Get(context.TODO(), netconf.IPAM.NeutronConfig, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("Error listing resources: %v", err)
	}

	nUUID, found, err := unstructured.NestedString(nConfig.Object, "spec", "networkUUID")
	if err != nil {
		return nil, fmt.Errorf("Error unstructured resources: %v", err)
	}

	if !found {
		return nil, fmt.Errorf("Error unstructured resources: invalid NeutronConfig %s", nConfig.GetName())
	}

	netconf.IPAM.NeutronUUID = nUUID
	return dynamicClient, nil

}

func GetPodsResource(dynamicClient *dynamic.DynamicClient, namespace string, PodName string) (*unstructured.Unstructured, error) {
	pod, err := dynamicClient.Resource(PodGVR).
		Namespace(namespace).
		Get(context.Background(), PodName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod: %v", err)
	}

	return pod, nil
}

func UpdatePodResource(dynamicClient *dynamic.DynamicClient, pod *unstructured.Unstructured) error {
	_, err := dynamicClient.Resource(PodGVR).
		Namespace(pod.GetNamespace()).
		Update(context.Background(), pod, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update pod resource: %v", err)
	}

	return nil
}
