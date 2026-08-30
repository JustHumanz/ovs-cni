#!/bin/bash
set -euo pipefail

NAMESPACE="kube-system"
CONFIGMAP_NAME="ovs-cni-ipam-kubeconfig"
DAEMONSET_NAME="ovs-cni-ipam-kubeconfig-installer"
SECRET_NAME="ovs-cni-ipam-token"

# ─────────────────────────────────────────────
# create_ovs_cni_ipam
#   Applies the IPAM resources, builds the kubeconfig
#   ConfigMap from the cluster's live credentials,
#   and deploys the installer DaemonSet.
# ─────────────────────────────────────────────
create_ovs_cni_ipam() {
  echo "==> [1/3] Applying IPAM resources..."
  kubectl apply -f examples/ovs-cni-ipam.yml

  echo "==> [2/3] Building kubeconfig from cluster credentials..."
  local SERVER CLUSTER_NAME CA_CERT TOKEN

  SERVER=$(kubectl -n "${NAMESPACE}" config view --minify \
    -o jsonpath='{.clusters[0].cluster.server}')
  CLUSTER_NAME=$(kubectl -n "${NAMESPACE}" config view --minify \
    -o jsonpath='{.clusters[0].name}')
  CA_CERT=$(kubectl -n "${NAMESPACE}" get secret "${SECRET_NAME}" \
    -o jsonpath='{.data.ca\.crt}')
  TOKEN=$(kubectl -n "${NAMESPACE}" get secret "${SECRET_NAME}" \
    -o jsonpath='{.data.token}' | base64 --decode)

  kubectl create -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: ${CONFIGMAP_NAME}
  namespace: ${NAMESPACE}
data:
  ovs-cni-ipam.kubeconfig: |
    apiVersion: v1
    kind: Config
    current-context: ovs-cni-ipam
    clusters:
    - name: ${CLUSTER_NAME}
      cluster:
        certificate-authority-data: ${CA_CERT}
        server: ${SERVER}
    contexts:
    - name: ovs-cni-ipam
      context:
        cluster: ${CLUSTER_NAME}
        user: ovs-cni-ipam
    users:
    - name: ovs-cni-ipam
      user:
        token: ${TOKEN}
EOF

  echo "==> [3/3] Deploying installer DaemonSet..."
  kubectl create -f - <<'EOF'
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: ovs-cni-ipam-kubeconfig-installer
  namespace: kube-system
  labels:
    app: ovs-cni-ipam-kubeconfig-installer
spec:
  selector:
    matchLabels:
      app: ovs-cni-ipam-kubeconfig-installer
  updateStrategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 1
  template:
    metadata:
      labels:
        app: ovs-cni-ipam-kubeconfig-installer
    spec:
      tolerations:
        - operator: Exists
          effect: NoSchedule
        - operator: Exists
          effect: NoExecute
      priorityClassName: system-node-critical
      initContainers:
        - name: install-kubeconfig
          image: busybox:1.36
          imagePullPolicy: IfNotPresent
          command:
            - sh
            - -c
            - |
              set -e
              TARGET_DIR=/host/etc/kubernetes/cni/net.d
              TARGET_FILE=$TARGET_DIR/ovs-cni-ipam.kubeconfig
              SOURCE_FILE=/config/ovs-cni-ipam.kubeconfig

              echo "Creating target directory: $TARGET_DIR"
              mkdir -p "$TARGET_DIR"

              echo "Copying kubeconfig to host..."
              cp "$SOURCE_FILE" "$TARGET_FILE"
              chmod 600 "$TARGET_FILE"

              echo "Done. Contents of $TARGET_DIR:"
              ls -la "$TARGET_DIR"
          volumeMounts:
            - name: kubeconfig-source
              mountPath: /config
              readOnly: true
            - name: host-root
              mountPath: /host
          securityContext:
            privileged: false
            runAsUser: 0
      containers:
        - name: pause
          image: busybox:1.36
          imagePullPolicy: IfNotPresent
          command:
            - sh
            - -c
            - |
              echo "kubeconfig installed. Watching for ConfigMap changes..."
              while true; do
                SOURCE=/config/ovs-cni-ipam.kubeconfig
                TARGET=/host/etc/kubernetes/cni/net.d/ovs-cni-ipam.kubeconfig
                if ! cmp -s "$SOURCE" "$TARGET"; then
                  echo "Detected change, updating $TARGET"
                  cp "$SOURCE" "$TARGET"
                  chmod 600 "$TARGET"
                fi
                sleep 60
              done
          volumeMounts:
            - name: kubeconfig-source
              mountPath: /config
              readOnly: true
            - name: host-root
              mountPath: /host
          securityContext:
            privileged: false
            runAsUser: 0
          resources:
            requests:
              cpu: 5m
              memory: 16Mi
            limits:
              cpu: 50m
              memory: 32Mi
      volumes:
        - name: kubeconfig-source
          configMap:
            name: ovs-cni-ipam-kubeconfig
            defaultMode: 0400
        - name: host-root
          hostPath:
            path: /
            type: Directory
      hostNetwork: false
      hostPID: false
      serviceAccountName: default
      terminationGracePeriodSeconds: 10
EOF

  echo "==> Done. DaemonSet '${DAEMONSET_NAME}' is deploying."
  echo "    Monitor with: kubectl -n ${NAMESPACE} rollout status daemonset/${DAEMONSET_NAME}"
}

# ─────────────────────────────────────────────
# delete_ovs_cni_ipam
#   Removes the DaemonSet, ConfigMap, and the
#   IPAM resources created by examples/ovs-cni-ipam.yml.
#   Passes --ignore-not-found so re-runs are safe.
# ─────────────────────────────────────────────
delete_ovs_cni_ipam() {
  echo "==> [1/3] Deleting installer DaemonSet..."
  kubectl delete daemonset "${DAEMONSET_NAME}" \
    -n "${NAMESPACE}" \
    --ignore-not-found

  echo "==> [2/3] Deleting kubeconfig ConfigMap..."
  kubectl delete configmap "${CONFIGMAP_NAME}" \
    -n "${NAMESPACE}" \
    --ignore-not-found

  echo "==> [3/3] Removing IPAM resources..."
  kubectl delete -f examples/ovs-cni-ipam.yml \
    --ignore-not-found

  echo "==> Done. All OVS CNI IPAM resources removed."
}

# ─────────────────────────────────────────────
# Entrypoint — call the right function based on
# the first argument: create | delete
# ─────────────────────────────────────────────
usage() {
  echo "Usage: $0 {create|delete}"
  exit 1
}

case "${1:-}" in
  create) create_ovs_cni_ipam ;;
  delete) delete_ovs_cni_ipam ;;
  *)      usage ;;
esac