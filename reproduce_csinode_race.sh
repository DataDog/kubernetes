#!/bin/bash

# Script to reproduce the CSINode race condition bug
# This script demonstrates the issue described in https://github.com/kubernetes/kubernetes/pull/131098

set -e

echo "=== CSINode Race Condition Reproducer ==="
echo "This script reproduces a race condition where CSINode objects are not recreated after node deletion/recreation"
echo

# Configuration
CLUSTER_NAME="repro-csinode"
CSI_DRIVER_REPO="/tmp/csi-driver-host-path"

# Function to wait for condition
wait_for_condition() {
    local condition="$1"
    local timeout="${2:-60}"
    local interval="${3:-5}"

    echo "Waiting for condition: $condition"
    for ((i=0; i<timeout; i+=interval)); do
        if eval "$condition"; then
            echo "Condition met!"
            return 0
        fi
        echo "Waiting... ($i/$timeout seconds)"
        sleep $interval
    done
    echo "Timeout waiting for condition: $condition"
    return 1
}

# Clean up any existing cluster
echo "0. Cleaning up any existing cluster..."
kind delete cluster --name $CLUSTER_NAME 2>/dev/null || true

# # Build kind image from local kubernetes/kubernetes
# echo "1. Building kind image from local kubernetes/kubernetes..."
# kind build node-image .

# Create kind cluster
echo "2. Creating kind cluster..."
kind create cluster --image kindest/node:latest --config kind-config.yaml

# Clone CSI driver repository if it doesn't exist
if [ ! -d "$CSI_DRIVER_REPO" ]; then
    echo "3. Cloning CSI hostpath driver repository..."
    git clone https://github.com/kubernetes-csi/csi-driver-host-path.git $CSI_DRIVER_REPO >/dev/null
fi

# Install VolumeSnapshot CRDs
echo "4. Installing VolumeSnapshot CRDs..."
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/v8.2.0/client/config/crd/snapshot.storage.k8s.io_volumesnapshotclasses.yaml >/dev/null
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/v8.2.0/client/config/crd/snapshot.storage.k8s.io_volumesnapshotcontents.yaml >/dev/null
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/v8.2.0/client/config/crd/snapshot.storage.k8s.io_volumesnapshots.yaml >/dev/null

# Deploy CSI driver
echo "5. Deploying CSI hostpath driver..."
$CSI_DRIVER_REPO/deploy/kubernetes-latest/deploy.sh &>/dev/null || {
    echo "Failed to deploy CSI hostpath driver. Please check the repository and try again."
    exit 1
}

# Apply snapshot class
kubectl apply -f $CSI_DRIVER_REPO/deploy/kubernetes-latest/hostpath/csi-hostpath-snapshotclass.yaml >/dev/null

# Wait for CSI plugin to be ready
echo "6. Waiting for CSI plugin to be ready..."
wait_for_condition "kubectl get pods csi-hostpathplugin-0 --no-headers | grep -q '8/8.*Running'" 120 10

# Show initial state
echo "7. Initial cluster state:"
echo "=== Nodes ==="
kubectl get nodes
echo "=== CSINodes ==="
kubectl get csinodes
echo "=== CSINode details for worker ==="
kubectl get csinode repro-csinode-worker -o yaml | grep -A 10 -B 5 ownerReferences

# Store the original CSINode UID for comparison
ORIGINAL_OWNERREF_UID=$(kubectl get csinode repro-csinode-worker -o jsonpath='{.metadata.ownerReferences[0].uid}')
ORIGINAL_NODE_UID=$(kubectl get node repro-csinode-worker -o jsonpath='{.metadata.uid}')
if [ "$ORIGINAL_OWNERREF_UID" == "$ORIGINAL_NODE_UID" ]; then
    echo "✓ Owner references match"
else
    echo "✗ Owner references do not match"
    exit 1
fi

# Simulate the race condition
echo "8. Simulating the race condition..."
echo "   a. Draining worker node..."
kubectl drain repro-csinode-worker --ignore-daemonsets --delete-emptydir-data --force

echo "   b. Deleting worker node from cluster..."
kubectl delete node repro-csinode-worker

echo "   c. Checking CSINode status immediately after node deletion..."
kubectl get csinodes || true

# Note: In a cluster with the modified garbage collector (1min delay),
# the CSINode should still exist here during the delay period

echo "   d. Restarting the worker node container to simulate node rejoin with same name..."
docker restart repro-csinode-worker

echo "   e. Waiting for node to rejoin cluster..."
wait_for_condition "kubectl get node repro-csinode-worker --no-headers 2>/dev/null | grep -q Ready" 60 5

echo "9. Final cluster state after node rejoin:"
echo "=== Nodes ==="
kubectl get nodes
echo "=== CSINodes ==="
kubectl get csinodes

# Check if CSINode was recreated with CSI driver
NEW_OWNERREF_UID=$(kubectl get csinode repro-csinode-worker -o jsonpath='{.metadata.ownerReferences[0].uid}' 2>/dev/null || echo "NOT_FOUND")
NEW_NODE_UID=$(kubectl get node repro-csinode-worker -o jsonpath='{.metadata.uid}' 2>/dev/null || echo "NOT_FOUND")
CSINODE_DRIVERS=$(kubectl get csinode repro-csinode-worker -o jsonpath='{.spec.drivers[*].name}' 2>/dev/null || echo "NONE")

echo "New CSINode OwnerRef UID: $NEW_OWNERREF_UID"
echo "CSI Drivers registered: $CSINODE_DRIVERS"

if [ "$NEW_OWNERREF_UID" != "$NEW_NODE_UID" ]; then
    echo "✗ Owner references don't match (fix didn't work)"
    exit 1
else
    echo "✓ CSINode was recreated (owner references match)"
fi

echo
echo "=== Reproducer Complete ==="
echo "To clean up: kind delete cluster --name $CLUSTER_NAME"
