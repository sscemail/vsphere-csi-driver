/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
	cnstypes "github.com/vmware/govmomi/cns/types"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	fnodes "k8s.io/kubernetes/test/e2e/framework/node"
	fpod "k8s.io/kubernetes/test/e2e/framework/pod"
	fpv "k8s.io/kubernetes/test/e2e/framework/pv"
	storage_utils "k8s.io/kubernetes/test/e2e/storage/utils"
)

var _ = ginkgo.Describe("Volume Expansion Test", func() {
	f := framework.NewDefaultFramework("volume-expansion")
	var (
		client              clientset.Interface
		namespace           string
		storagePolicyName   string
		profileID           string
		pandoraSyncWaitTime int
		defaultDatastore    *object.Datastore
	)
	ginkgo.BeforeEach(func() {
		client = f.ClientSet
		namespace = f.Namespace.Name
		bootstrap()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		nodeList, err := fnodes.GetReadySchedulableNodes(f.ClientSet)
		framework.ExpectNoError(err, "Unable to find ready and schedulable Node")
		if !(len(nodeList.Items) > 0) {
			framework.Failf("Unable to find ready and schedulable Node")
		}

		if os.Getenv(envPandoraSyncWaitTime) != "" {
			pandoraSyncWaitTime, err = strconv.Atoi(os.Getenv(envPandoraSyncWaitTime))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		} else {
			pandoraSyncWaitTime = defaultPandoraSyncWaitTime
		}
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		defaultDatastore = getDefaultDatastore(ctx)

	})

	// Test to verify volume expansion is supported if allowVolumeExpansion
	// is true in StorageClass, PVC is created and offline and not attached
	// to a Pod before the expansion.

	// Steps
	// 1. Create StorageClass with allowVolumeExpansion set to true.
	// 2. Create PVC which uses the StorageClass created in step 1.
	// 3. Wait for PV to be provisioned.
	// 4. Wait for PVC's status to become Bound.
	// 5. Modify PVC's size to trigger offline volume expansion.
	// 6. Create pod using PVC on specific node.
	// 7. Wait for Disk to be attached to the node.
	// 8. Wait for file system resize to complete.
	// 9. Delete pod and Wait for Volume Disk to be detached from the Node.
	// 10. Delete PVC, PV and Storage Class.

	ginkgo.It("[csi-block-vanilla] [csi-guest] Verify volume expansion with no filesystem before expansion", func() {
		invokeTestForVolumeExpansion(f, client, namespace, "", storagePolicyName, profileID)
	})

	// Test to verify volume expansion is supported if allowVolumeExpansion
	// is true in StorageClass, PVC is created, attached to a Pod, detached,
	// expanded, and attached to a Pod again to finish the filesystem resize.

	// Steps
	// 1. Create StorageClass with allowVolumeExpansion set to true.
	// 2. Create PVC which uses the StorageClass created in step 1.
	// 3. Wait for PV to be provisioned.
	// 4. Wait for PVC's status to become Bound.
	// 5. Create pod using PVC on specific node.
	// 6. Wait for Disk to be attached to the node.
	// 7. Detach the volume.
	// 8. Modify PVC's size to trigger offline volume expansion.
	// 9. Create pod again using PVC on specific node.
	// 10. Wait for Disk to be attached to the node.
	// 11. Wait for file system resize to complete.
	// 12. Delete pod and Wait for Volume Disk to be detached from the Node.
	// 13. Delete PVC, PV and Storage Class.

	ginkgo.It("[csi-block-vanilla] [csi-guest] Verify volume expansion with initial filesystem before expansion", func() {
		invokeTestForVolumeExpansionWithFilesystem(f, client, namespace, "", storagePolicyName, profileID)
	})

	// Test to verify volume expansion is not supported if allowVolumeExpansion
	// is false in StorageClass.

	// Steps
	// 1. Create StorageClass with allowVolumeExpansion set to false.
	// 2. Create PVC which uses the StorageClass created in step 1.
	// 3. Wait for PV to be provisioned.
	// 4. Wait for PVC's status to become Bound.
	// 5. Modify PVC's size to trigger offline volume expansion.
	// 6. Verify if the PVC expansion fails.

	ginkgo.It("[csi-block-vanilla] [csi-guest] Verify volume expansion not allowed", func() {
		invokeTestForInvalidVolumeExpansion(f, client, namespace, storagePolicyName, profileID)
	})

	// Test to verify volume expansion is not supported if allowVolumeExpansion
	// is true in StorageClass but the volume is online.

	// Steps
	// 1. Create StorageClass with allowVolumeExpansion set to true.
	// 2. Create PVC which uses the StorageClass created in step 1.
	// 3. Wait for PV to be provisioned.
	// 4. Wait for PVC's status to become Bound.
	// 5. Create pod using PVC on specific node.
	// 6. Wait for Disk to be attached to the node.
	// 7. Modify PVC's size to trigger volume expansion.
	// 8. Verify if the PVC expansion fails because online expansion is not supported.

	ginkgo.It("[csi-block-vanilla] [csi-guest] Verify online volume expansion not allowed", func() {
		invokeTestForInvalidOnlineVolumeExpansion(f, client, namespace, "", storagePolicyName, profileID)
	})

	// Test to verify volume expansion is not supported if new size is
	// smaller than the current size.

	// Steps
	// 1. Create StorageClass with allowVolumeExpansion set to true.
	// 2. Create PVC which uses the StorageClass created in step 1.
	// 3. Wait for PV to be provisioned.
	// 4. Wait for PVC's status to become Bound.
	// 5. Modify PVC's size to a smaller size.
	// 6. Verify if the PVC expansion fails.

	ginkgo.It("[csi-block-vanilla] [csi-guest] Verify volume shrinking not allowed", func() {
		invokeTestForInvalidVolumeShrink(f, client, namespace, storagePolicyName, profileID)
	})

	// Test to verify volume expansion is not support for static provisioning.

	// Steps
	// 1. Create FCD and wait for fcd to allow syncing with pandora.
	// 2. Create PV Spec with volumeID set to FCDID created in Step-1, and
	//    PersistentVolumeReclaimPolicy is set to Delete.
	// 3. Create PVC with the storage request set to PV's storage capacity.
	// 4. Wait for PV and PVC to bound.
	// 5. Modify size of PVC to trigger volume expansion.
	// 6. It should fail because volume expansion is not supported.
	// 7. Delete PVC.
	// 8. Verify PV is deleted automatically.

	ginkgo.It("[csi-block-vanilla] Verify volume expansion is not supported for static provisioning", func() {
		invokeTestForInvalidVolumeExpansionStaticProvision(f, client, namespace, storagePolicyName, profileID)
	})

	// Test to verify volume expansion can happen multiple times

	// Steps
	// 1. Create StorageClass with allowVolumeExpansion set to true.
	// 2. Create PVC which uses the StorageClass created in step 1.
	// 3. Wait for PV to be provisioned.
	// 4. Wait for PVC's status to become Bound.
	// 5. In a loop of 10, modify PVC's size by adding 1 Gb at a time
	//    to trigger offline volume expansion.
	// 6. Create pod using PVC on specific node.
	// 7. Wait for Disk to be attached to the node.
	// 8. Wait for file system resize to complete.
	// 9. Delete pod and Wait for Volume Disk to be detached from the Node.
	// 10. Delete PVC, PV and Storage Class.

	ginkgo.It("[csi-block-vanilla] [csi-guest] Verify volume expansion can happen multiple times", func() {
		invokeTestForExpandVolumeMultipleTimes(f, client, namespace, "", storagePolicyName, profileID)
	})

	// Test to verify volume expansion is not supported for file volume

	// Steps
	// 1. Create StorageClass with allowVolumeExpansion set to true.
	// 2. Create File Volume PVC which uses the StorageClass created in step 1.
	// 3. Wait for PV to be provisioned.
	// 4. Wait for PVC's status to become Bound.
	// 5. Modify PVC's size to a bigger size.
	// 6. Verify if the PVC expansion fails.
	ginkgo.It("[csi-file-vanilla] Verify file volume expansion is not supported", func() {
		invokeTestForUnsupportedFileVolumeExpansion(f, client, namespace, storagePolicyName, profileID)
	})

	/*
		Verify online volume expansion on dynamic volume

		1. Create StorageClass with allowVolumeExpansion set to true.
		2. Create PVC which uses the StorageClass created in step 1.
		3. Wait for PV to be provisioned.
		4. Wait for PVC's status to become Bound and note down the size
		5. Create a POD using the above created PVC
		6. Modify PVC's size to trigger online volume expansion
		7. verify the PVC status will change to "FilesystemResizePending". Wait till the status is removed
		8. Verify the resized PVC by doing CNS query
		9. Make sure data is intact on the PV mounted on the pod
		10.  Make sure file system has increased

	*/
	ginkgo.It("[csi-block-vanilla] Verify online volume expansion on dynamic volume", func() {
		ginkgo.By("Invoking Test for Volume Expansion")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		ginkgo.By("Create StorageClass with allowVolumeExpansion set to true, Create PVC")
		volHandle, pvclaim, pv, storageclass := createSCwithVolumeExpansionTrueAndDynamicPVC(f, client, namespace)
		defer func() {
			err := client.StorageV1().StorageClasses().Delete(ctx, storageclass.Name, *metav1.NewDeleteOptions(0))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			err = fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			err = e2eVSphere.waitForCNSVolumeToBeDeleted(pv.Spec.CSI.VolumeHandle)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}()

		ginkgo.By("Create POD using the above PVC")
		pod := createPODandVerifyVolumeMount(f, client, namespace, pvclaim, volHandle)

		defer func() {
			// Delete POD
			ginkgo.By(fmt.Sprintf("Deleting the pod %s in namespace %s", pod.Name, namespace))
			err := fpod.DeletePodWithWait(client, pod)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("Verify volume is detached from the node")
			isDiskDetached, err := e2eVSphere.waitForVolumeDetachedFromNode(client, pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(isDiskDetached).To(gomega.BeTrue(), fmt.Sprintf("Volume %q is not detached from the node %q", pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName))
		}()

		ginkgo.By("Increase PVC size and verify Volume resize")
		increaseSizeOfPvcAttachedToPod(f, client, namespace, pvclaim, pod)
	})

	/*
		Verify online volume expansion on static volume

		1. Create FCD and wait for fcd to allow syncing with pandora.
		2. Create PV Spec with volumeID set to FCDID created in Step-1, and PersistentVolumeReclaimPolicy is set to Delete.
		3. Create PVC with the storage request set to PV's storage capacity.
		4. Wait for PV and PVC to bound and note the PVC size
		5. Create POD using above created PVC
		6. Modify size of PVC to trigger online volume expansion.
		7. verify the PVC status will change to "FilesystemResizePending". Wait till the status is removed
		8. Verify the resized PVC by doing CNS query
		9. Make sure data is intact on the PV mounted on the pod
		10. Make sure file system has increased

	*/
	ginkgo.It("[csi-block-vanilla] Verify online volume expansion on static volume", func() {
		ginkgo.By("Invoking Test for Volume Expansion on statically created PVC ")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		volHandle, pvclaim, pv, storageclass := createStaticPVC(ctx, f, client, namespace, defaultDatastore, pandoraSyncWaitTime)

		defer func() {
			err := client.StorageV1().StorageClasses().Delete(ctx, storageclass.Name, *metav1.NewDeleteOptions(0))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			err = fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			err = e2eVSphere.waitForCNSVolumeToBeDeleted(pv.Spec.CSI.VolumeHandle)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}()

		ginkgo.By("Create POD")
		pod := createPODandVerifyVolumeMount(f, client, namespace, pvclaim, volHandle)

		defer func() {
			// Delete POD
			ginkgo.By(fmt.Sprintf("Deleting the pod %s in namespace %s", pod.Name, namespace))
			err := fpod.DeletePodWithWait(client, pod)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("Verify volume is detached from the node")
			isDiskDetached, err := e2eVSphere.waitForVolumeDetachedFromNode(client, pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(isDiskDetached).To(gomega.BeTrue(), fmt.Sprintf("Volume %q is not detached from the node %q", pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName))
		}()

		increaseSizeOfPvcAttachedToPod(f, client, namespace, pvclaim, pod)
	})

	/*
		Verify online volume expansion does not support shrinking volume

		1. Create StorageClass with allowVolumeExpansion set to true.
		2. Create PVC which uses the StorageClass created in step 1.
		3. Wait for PV to be provisioned.
		4. Wait for PVC's status to become Bound.
		5. Create POD
		6. Modify PVC to be a smaller size.
		7. Verify that the PVC size does not change because volume shrinking is not supported.
	*/
	ginkgo.It("[csi-block-vanilla] Verify online volume expansion shrinking volume not allowed", func() {
		ginkgo.By("Invoking Test for Volume Expansion")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		volHandle, pvclaim, pv, storageclass := createSCwithVolumeExpansionTrueAndDynamicPVC(f, client, namespace)
		defer func() {
			err := client.StorageV1().StorageClasses().Delete(ctx, storageclass.Name, *metav1.NewDeleteOptions(0))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			err = fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			err = e2eVSphere.waitForCNSVolumeToBeDeleted(pv.Spec.CSI.VolumeHandle)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}()

		ginkgo.By("Create POD")
		pod := createPODandVerifyVolumeMount(f, client, namespace, pvclaim, volHandle)

		defer func() {
			// Delete POD
			ginkgo.By(fmt.Sprintf("Deleting the pod %s in namespace %s", pod.Name, namespace))
			err := fpod.DeletePodWithWait(client, pod)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("Verify volume is detached from the node")
			isDiskDetached, err := e2eVSphere.waitForVolumeDetachedFromNode(client, pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(isDiskDetached).To(gomega.BeTrue(), fmt.Sprintf("Volume %q is not detached from the node %q", pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName))
		}()

		// Modify PVC spec to a smaller size
		// Expect this to fail
		ginkgo.By("Verify operation will fail because volume shrinking is not supported")
		currentPvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
		newSize := currentPvcSize.DeepCopy()
		newSize.Sub(resource.MustParse("100Mi"))
		framework.Logf("currentPvcSize %v, newSize %v", currentPvcSize, newSize)
		_, err := expandPVCSize(pvclaim, newSize, client)
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	/*
			Verify online volume expansion multiple times on the same PVC

			1. Create StorageClass with allowVolumeExpansion set to true.
			2. Create PVC which uses the StorageClass created in step 1.
			3. Wait for PV to be provisioned.
		    4. Wait for PVC's status to become Bound and note the PVC size
			5. Create POD using the above created PVC
			6. In a loop of 10, modify PVC's size by adding 1 Gb at a time to trigger online volume expansion.
			7. Wait for file system resize to complete.
			8. Verify the PVC Size should increased by 10Gi
			9. Make sure file system has increased
	*/
	ginkgo.It("[csi-block-vanilla] Verify online volume expansion multiple times on the same PVC", func() {

		ginkgo.By("Invoking Test for Volume Expansion")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		volHandle, pvclaim, pv, storageclass := createSCwithVolumeExpansionTrueAndDynamicPVC(f, client, namespace)
		defer func() {
			err := client.StorageV1().StorageClasses().Delete(ctx, storageclass.Name, *metav1.NewDeleteOptions(0))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			err = fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			err = e2eVSphere.waitForCNSVolumeToBeDeleted(pv.Spec.CSI.VolumeHandle)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}()

		pod := createPODandVerifyVolumeMount(f, client, namespace, pvclaim, volHandle)

		defer func() {
			// Delete POD
			ginkgo.By(fmt.Sprintf("Deleting the pod %s in namespace %s", pod.Name, namespace))
			err := fpod.DeletePodWithWait(client, pod)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("Verify volume is detached from the node")
			isDiskDetached, err := e2eVSphere.waitForVolumeDetachedFromNode(client, pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(isDiskDetached).To(gomega.BeTrue(), fmt.Sprintf("Volume %q is not detached from the node %q", pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName))
		}()

		increaseOnlineVolumeMultipleTimes(ctx, f, client, namespace, volHandle, pvclaim, pod)
	})

})

//increaseOnlineVolumeMultipleTimes this method increases the same volume multiple times and verifies PVC and Filesystem size
func increaseOnlineVolumeMultipleTimes(ctx context.Context, f *framework.Framework, client clientset.Interface, namespace string, volHandle string, pvclaim *v1.PersistentVolumeClaim, pod *v1.Pod) {

	//Get original FileSystem size
	originalSizeInMb, err := getFSSizeMb(f, pod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// Modify PVC spec to trigger volume expansion
	ginkgo.By("Expanding pvc 10 times")
	currentPvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
	newSize := currentPvcSize.DeepCopy()
	for i := 0; i < 10; i++ {
		newSize.Add(resource.MustParse("1Gi"))
		ginkgo.By(fmt.Sprintf("Expanding pvc to new size: %v", newSize))
		pvclaim, err := expandPVCSize(pvclaim, newSize, client)
		framework.ExpectNoError(err, "While updating pvc for more size")
		gomega.Expect(pvclaim).NotTo(gomega.BeNil())

		pvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
		if pvcSize.Cmp(newSize) != 0 {
			framework.Failf("error updating pvc %q to size %v", pvclaim.Name, newSize)
		}
	}

	ginkgo.By("Waiting for controller resize to finish")
	framework.Logf("PVC name : " + pvclaim.Name)
	pv := getPvFromClaim(client, namespace, pvclaim.Name)
	pvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
	err = waitForPvResize(pv, client, pvcSize, totalResizeWaitPeriod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("Checking for conditions on pvc")
	framework.Logf("PVC Name :", pvclaim.Name)
	pvclaim, err = waitForPVCToReachFileSystemResizePendingCondition(client, namespace, pvclaim.Name, totalResizeWaitPeriod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By(fmt.Sprintf("Invoking QueryCNSVolumeWithResult with VolumeID: %s", volHandle))
	queryResult, err := e2eVSphere.queryCNSVolumeWithResult(volHandle)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	if len(queryResult.Volumes) == 0 {
		err = fmt.Errorf("QueryCNSVolumeWithResult returned no volume")
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	ginkgo.By("Verifying disk size requested in volume expansion is honored")
	newSizeInMb := int64(12288)
	if queryResult.Volumes[0].BackingObjectDetails.(*cnstypes.CnsBlockBackingDetails).CapacityInMb != newSizeInMb {
		err = fmt.Errorf("Received wrong disk size after volume expansion. Expected: %d Actual: %d", newSizeInMb, queryResult.Volumes[0].BackingObjectDetails.(*cnstypes.CnsBlockBackingDetails).CapacityInMb)
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	pvclaim, err = waitForFSResize(pvclaim, client)
	framework.ExpectNoError(err, "while waiting for fs resize to finish")

	pvcConditions := pvclaim.Status.Conditions
	expectEqual(len(pvcConditions), 0, "pvc should not have conditions")

	ginkgo.By("Verify filesystem size for mount point /mnt/volume1")
	fsSize, err := getFSSizeMb(f, pod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	// Filesystem size may be smaller than the size of the block volume
	// so here we are checking if the new filesystem size is greater than
	// the original volume size as the filesystem is formatted.
	if fsSize < originalSizeInMb {
		framework.Failf("error updating filesystem size for %q. Resulting filesystem size is %d mb", pvclaim.Name, fsSize)
	}
	framework.Logf("File system resize finished successfully %d mb", fsSize)

}

//createStaticPVC this method creates static PVC
func createStaticPVC(ctx context.Context, f *framework.Framework, client clientset.Interface, namespace string, defaultDatastore *object.Datastore, pandoraSyncWaitTime int) (string, *v1.PersistentVolumeClaim, *v1.PersistentVolume, *storagev1.StorageClass) {
	curtime := time.Now().Unix()

	sc, err := createStorageClass(client, nil, nil, "", "", true, "")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	framework.Logf("Storage Class Name :%s", sc.Name)

	ginkgo.By("Creating FCD Disk")
	curtimeinstring := strconv.FormatInt(curtime, 10)
	fcdID, err := e2eVSphere.createFCD(ctx, "BasicStaticFCD"+curtimeinstring, diskSizeInMb, defaultDatastore.Reference())
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	framework.Logf("FCD ID :", fcdID)

	ginkgo.By(fmt.Sprintf("Sleeping for %v seconds to allow newly created FCD:%s to sync with pandora", pandoraSyncWaitTime, fcdID))
	time.Sleep(time.Duration(pandoraSyncWaitTime) * time.Second)

	// Creating label for PV.
	// PVC will use this label as Selector to find PV
	staticPVLabels := make(map[string]string)
	staticPVLabels["fcd-id"] = fcdID

	ginkgo.By("Creating PV")
	pv := getPersistentVolumeSpecWithStorageclass(fcdID, v1.PersistentVolumeReclaimDelete, sc.Name, nil)
	pv, err = client.CoreV1().PersistentVolumes().Create(ctx, pv, metav1.CreateOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	pvName := pv.GetName()

	ginkgo.By("Creating PVC")
	pvc := getPVCSpecWithPVandStorageClass("static-pvc", namespace, nil, pvName, sc.Name)
	pvc, err = client.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("Waiting for claim to be in bound phase")
	err = fpv.WaitForPersistentVolumeClaimPhase(v1.ClaimBound, client, namespace, pvc.Name, framework.Poll, framework.ClaimProvisionTimeout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("Verifying CNS entry is present in cache")
	_, err = e2eVSphere.queryCNSVolumeWithResult(pv.Spec.CSI.VolumeHandle)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	verifyBidirectionalReferenceOfPVandPVC(ctx, client, pvc, pv, fcdID)

	volHandle := pv.Spec.CSI.VolumeHandle

	// Wait for PV and PVC to Bind
	framework.ExpectNoError(fpv.WaitOnPVandPVC(client, namespace, pv, pvc))

	return volHandle, pvc, pv, sc
}

//createSCwithVolumeExpansionTrueAndDynamicPVC creates storageClass with allowVolumeExpansion set to true and Creates PVC. Waits till PV, PVC are in bound
func createSCwithVolumeExpansionTrueAndDynamicPVC(f *framework.Framework, client clientset.Interface, namespace string) (string, *v1.PersistentVolumeClaim, *v1.PersistentVolume, *storagev1.StorageClass) {
	scParameters := make(map[string]string)
	scParameters[scParamFsType] = ext4FSType

	// Create Storage class and PVC
	ginkgo.By("Creating Storage Class and PVC with allowVolumeExpansion = true")
	var storageclass *storagev1.StorageClass
	var pvclaim *v1.PersistentVolumeClaim
	var err error

	storageclass, pvclaim, err = createPVCAndStorageClass(client, namespace, nil, scParameters, "", nil, "", true, "")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// Waiting for PVC to be bound
	var pvclaims []*v1.PersistentVolumeClaim
	pvclaims = append(pvclaims, pvclaim)
	ginkgo.By("Waiting for all claims to be in bound state")
	persistentvolumes, err := fpv.WaitForPVClaimBoundPhase(client, pvclaims, framework.ClaimProvisionTimeout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	pv := persistentvolumes[0]
	volHandle := pv.Spec.CSI.VolumeHandle

	return volHandle, pvclaims[0], pv, storageclass

}

//createPODandVerifyVolumeMount this method creates POD and verifies VolumeMount
func createPODandVerifyVolumeMount(f *framework.Framework, client clientset.Interface, namespace string, pvclaim *v1.PersistentVolumeClaim, volHandle string) *v1.Pod {
	// Create a POD to use this PVC, and verify volume has been attached
	ginkgo.By("Creating pod to attach PV to the node")
	pod, err := createPod(client, namespace, nil, []*v1.PersistentVolumeClaim{pvclaim}, false, execCommand)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	var vmUUID string
	ginkgo.By(fmt.Sprintf("Verify volume: %s is attached to the node: %s", volHandle, pod.Spec.NodeName))
	vmUUID = getNodeUUID(client, pod.Spec.NodeName)

	isDiskAttached, err := e2eVSphere.isVolumeAttachedToVM(client, volHandle, vmUUID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(isDiskAttached).To(gomega.BeTrue(), "Volume is not attached to the node volHandle: %s, vmUUID: %s", volHandle, vmUUID)

	ginkgo.By("Verify the volume is accessible and filesystem type is as expected")
	_, err = framework.LookForStringInPodExec(namespace, pod.Name, []string{"/bin/cat", "/mnt/volume1/fstype"}, "", time.Minute)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return pod
}

//increaseSizeOfPvcAttachedToPod this method increases the PVC size, which is attached to POD
func increaseSizeOfPvcAttachedToPod(f *framework.Framework, client clientset.Interface, namespace string, pvclaim *v1.PersistentVolumeClaim, pod *v1.Pod) {

	//Fetch original FileSystemSize
	originalSizeInMb, err := getFSSizeMb(f, pod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	//resize PVC
	// Modify PVC spec to trigger volume expansion
	ginkgo.By("Expanding current pvc")
	currentPvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
	newSize := currentPvcSize.DeepCopy()
	newSize.Add(resource.MustParse("1Gi"))
	framework.Logf("currentPvcSize %v, newSize %v", currentPvcSize, newSize)
	pvclaim, err = expandPVCSize(pvclaim, newSize, client)
	framework.ExpectNoError(err, "While updating pvc for more size")
	gomega.Expect(pvclaim).NotTo(gomega.BeNil())

	ginkgo.By("Waiting for file system resize to finish")
	pvclaim, err = waitForFSResize(pvclaim, client)
	framework.ExpectNoError(err, "while waiting for fs resize to finish")

	pvcConditions := pvclaim.Status.Conditions
	expectEqual(len(pvcConditions), 0, "pvc should not have conditions")

	ginkgo.By("Verify filesystem size for mount point /mnt/volume1")
	fsSize, err := getFSSizeMb(f, pod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	// Filesystem size may be smaller than the size of the block volume
	// so here we are checking if the new filesystem size is greater than
	// the original volume size as the filesystem is formatted for the
	// first time
	if fsSize < originalSizeInMb {
		framework.Failf("error updating filesystem size for %q. Resulting filesystem size is %d mb", pvclaim.Name, fsSize)
	}
	ginkgo.By("File system resize finished successfully")
}

func invokeTestForVolumeExpansion(f *framework.Framework, client clientset.Interface, namespace string, expectedContent string, storagePolicyName string, profileID string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ginkgo.By("Invoking Test for Volume Expansion")
	scParameters := make(map[string]string)
	scParameters[scParamFsType] = ext4FSType
	// Create Storage class and PVC
	ginkgo.By("Creating Storage Class and PVC with allowVolumeExpansion = true")
	var storageclass *storagev1.StorageClass
	var pvclaim *v1.PersistentVolumeClaim
	var err error

	// Create a StorageClass that sets allowVolumeExpansion to true
	if guestCluster {
		storagePolicyNameForSharedDatastores := GetAndExpectStringEnvVar(envStoragePolicyNameForSharedDatastores)
		scParameters[svStorageClassName] = storagePolicyNameForSharedDatastores
	}
	storageclass, pvclaim, err = createPVCAndStorageClass(client, namespace, nil, scParameters, "", nil, "", true, "")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	defer func() {
		err := client.StorageV1().StorageClasses().Delete(ctx, storageclass.Name, *metav1.NewDeleteOptions(0))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()
	defer func() {
		err := fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()

	// Waiting for PVC to be bound
	var pvclaims []*v1.PersistentVolumeClaim
	pvclaims = append(pvclaims, pvclaim)
	ginkgo.By("Waiting for all claims to be in bound state")
	persistentvolumes, err := fpv.WaitForPVClaimBoundPhase(client, pvclaims, framework.ClaimProvisionTimeout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	pv := persistentvolumes[0]
	volHandle := pv.Spec.CSI.VolumeHandle
	svcPVCName := pv.Spec.CSI.VolumeHandle
	if guestCluster {
		volHandle = getVolumeIDFromSupervisorCluster(volHandle)
		gomega.Expect(volHandle).NotTo(gomega.BeEmpty())
	}

	defer func() {
		err := fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		err = e2eVSphere.waitForCNSVolumeToBeDeleted(pv.Spec.CSI.VolumeHandle)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()

	// Modify PVC spec to trigger volume expansion
	// We expand the PVC while no pod is using it to ensure offline expansion
	ginkgo.By("Expanding current pvc")
	currentPvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
	newSize := currentPvcSize.DeepCopy()
	newSize.Add(resource.MustParse("1Gi"))
	framework.Logf("currentPvcSize %v, newSize %v", currentPvcSize, newSize)
	pvclaim, err = expandPVCSize(pvclaim, newSize, client)
	framework.ExpectNoError(err, "While updating pvc for more size")
	gomega.Expect(pvclaim).NotTo(gomega.BeNil())

	pvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
	if pvcSize.Cmp(newSize) != 0 {
		framework.Failf("error updating pvc size %q", pvclaim.Name)
	}
	if guestCluster {
		ginkgo.By("Checking for PVC request size change on SVC PVC")
		b, err := verifyPvcRequestedSizeUpdateInSupervisorWithWait(svcPVCName, newSize)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(b).To(gomega.BeTrue())
	}

	ginkgo.By("Waiting for controller volume resize to finish")
	err = waitForPvResizeForGivenPvc(pvclaim, client, totalResizeWaitPeriod)
	framework.ExpectNoError(err, "While waiting for pvc resize to finish")

	if guestCluster {
		ginkgo.By("Checking for resize on SVC PV")
		verifyPVSizeinSupervisor(svcPVCName, newSize)
	}

	ginkgo.By("Checking for conditions on pvc")
	pvclaim, err = waitForPVCToReachFileSystemResizePendingCondition(client, namespace, pvclaim.Name, pollTimeout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	if guestCluster {
		ginkgo.By("Checking for 'FileSystemResizePending' status condition on SVC PVC")
		_, err = checkSvcPvcHasGivenStatusCondition(pv.Spec.CSI.VolumeHandle, true, v1.PersistentVolumeClaimFileSystemResizePending)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}

	ginkgo.By(fmt.Sprintf("Invoking QueryCNSVolumeWithResult with VolumeID: %s", volHandle))
	queryResult, err := e2eVSphere.queryCNSVolumeWithResult(volHandle)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	if len(queryResult.Volumes) == 0 {
		err = fmt.Errorf("QueryCNSVolumeWithResult returned no volume")
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	ginkgo.By("Verifying disk size requested in volume expansion is honored")
	newSizeInMb := int64(3072)
	if queryResult.Volumes[0].BackingObjectDetails.(*cnstypes.CnsBlockBackingDetails).CapacityInMb != newSizeInMb {
		err = fmt.Errorf("Got wrong disk size after volume expansion")
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// Create a POD to use this PVC, and verify volume has been attached
	ginkgo.By("Creating pod to attach PV to the node")
	pod, err := createPod(client, namespace, nil, []*v1.PersistentVolumeClaim{pvclaim}, false, execCommand)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	var vmUUID string
	ginkgo.By(fmt.Sprintf("Verify volume: %s is attached to the node: %s", volHandle, pod.Spec.NodeName))
	vmUUID = getNodeUUID(client, pod.Spec.NodeName)
	if guestCluster {
		vmUUID, err = getVMUUIDFromNodeName(pod.Spec.NodeName)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}
	isDiskAttached, err := e2eVSphere.isVolumeAttachedToVM(client, volHandle, vmUUID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(isDiskAttached).To(gomega.BeTrue(), "Volume is not attached to the node")

	ginkgo.By("Verify the volume is accessible and filesystem type is as expected")
	_, err = framework.LookForStringInPodExec(namespace, pod.Name, []string{"/bin/cat", "/mnt/volume1/fstype"}, expectedContent, time.Minute)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("Waiting for file system resize to finish")
	pvclaim, err = waitForFSResize(pvclaim, client)
	framework.ExpectNoError(err, "while waiting for fs resize to finish")

	pvcConditions := pvclaim.Status.Conditions
	expectEqual(len(pvcConditions), 0, "pvc should not have conditions")

	ginkgo.By("Verify filesystem size for mount point /mnt/volume1")
	fsSize, err := getFSSizeMb(f, pod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	// Filesystem size may be smaller than the size of the block volume
	// so here we are checking if the new filesystem size is greater than
	// the original volume size as the filesystem is formatted for the
	// first time after pod creation
	if fsSize < diskSizeInMb {
		framework.Failf("error updating filesystem size for %q. Resulting filesystem size is %d", pvclaim.Name, fsSize)
	}
	ginkgo.By("File system resize finished successfully")

	if guestCluster {
		ginkgo.By("Checking for PVC resize completion on SVC PVC")
		gomega.Expect(verifyResizeCompletedInSupervisor(svcPVCName)).To(gomega.BeTrue())
	}

	// Delete POD
	ginkgo.By(fmt.Sprintf("Deleting the pod %s in namespace %s", pod.Name, namespace))
	err = fpod.DeletePodWithWait(client, pod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("Verify volume is detached from the node")
	isDiskDetached, err := e2eVSphere.waitForVolumeDetachedFromNode(client, pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(isDiskDetached).To(gomega.BeTrue(), fmt.Sprintf("Volume %q is not detached from the node %q", pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName))
}

func invokeTestForVolumeExpansionWithFilesystem(f *framework.Framework, client clientset.Interface, namespace string, expectedContent string, storagePolicyName string, profileID string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ginkgo.By("Invoking Test for Volume Expansion 2")
	scParameters := make(map[string]string)
	scParameters[scParamFsType] = ext4FSType
	// Create Storage class and PVC
	ginkgo.By("Creating Storage Class and PVC with allowVolumeExpansion = true")
	var storageclass *storagev1.StorageClass
	var pvclaim *v1.PersistentVolumeClaim
	var err error

	// Create a StorageClass that sets allowVolumeExpansion to true
	if guestCluster {
		storagePolicyNameForSharedDatastores := GetAndExpectStringEnvVar(envStoragePolicyNameForSharedDatastores)
		scParameters[svStorageClassName] = storagePolicyNameForSharedDatastores
	}
	storageclass, pvclaim, err = createPVCAndStorageClass(client, namespace, nil, scParameters, "", nil, "", true, "")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	defer func() {
		err := client.StorageV1().StorageClasses().Delete(ctx, storageclass.Name, *metav1.NewDeleteOptions(0))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()
	defer func() {
		err := fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()

	// Waiting for PVC to be bound
	var pvclaims []*v1.PersistentVolumeClaim
	pvclaims = append(pvclaims, pvclaim)
	ginkgo.By("Waiting for all claims to be in bound state")
	persistentvolumes, err := fpv.WaitForPVClaimBoundPhase(client, pvclaims, framework.ClaimProvisionTimeout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	pv := persistentvolumes[0]
	volHandle := pv.Spec.CSI.VolumeHandle
	svcPVCName := pv.Spec.CSI.VolumeHandle
	if guestCluster {
		volHandle = getVolumeIDFromSupervisorCluster(volHandle)
		gomega.Expect(volHandle).NotTo(gomega.BeEmpty())
	}

	defer func() {
		err := fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		err = e2eVSphere.waitForCNSVolumeToBeDeleted(pv.Spec.CSI.VolumeHandle)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()

	// Create a POD to use this PVC, and verify volume has been attached
	ginkgo.By("Creating pod to attach PV to the node")
	pod, err := createPod(client, namespace, nil, []*v1.PersistentVolumeClaim{pvclaim}, false, execCommand)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	var vmUUID string
	ginkgo.By(fmt.Sprintf("Verify volume: %s is attached to the node: %s", pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName))
	vmUUID = getNodeUUID(client, pod.Spec.NodeName)
	if guestCluster {
		vmUUID, err = getVMUUIDFromNodeName(pod.Spec.NodeName)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}
	isDiskAttached, err := e2eVSphere.isVolumeAttachedToVM(client, volHandle, vmUUID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(isDiskAttached).To(gomega.BeTrue(), "Volume is not attached to the node")

	ginkgo.By("Verify the volume is accessible and filesystem type is as expected")
	_, err = framework.LookForStringInPodExec(namespace, pod.Name, []string{"/bin/cat", "/mnt/volume1/fstype"}, expectedContent, time.Minute)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("Check filesystem size for mount point /mnt/volume1 before expansion")
	originalFsSize, err := getFSSizeMb(f, pod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// Delete POD
	ginkgo.By(fmt.Sprintf("Deleting the pod %s in namespace %s", pod.Name, namespace))
	err = fpod.DeletePodWithWait(client, pod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("Verify volume is detached from the node")
	isDiskDetached, err := e2eVSphere.waitForVolumeDetachedFromNode(client, pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(isDiskDetached).To(gomega.BeTrue(), fmt.Sprintf("Volume %q is not detached from the node %q", pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName))

	// Modify PVC spec to trigger volume expansion
	// We expand the PVC while no pod is using it to ensure offline expansion
	ginkgo.By("Expanding current pvc")
	currentPvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
	newSize := currentPvcSize.DeepCopy()
	newSize.Add(resource.MustParse("1Gi"))
	framework.Logf("currentPvcSize %v, newSize %v", currentPvcSize, newSize)
	pvclaim, err = expandPVCSize(pvclaim, newSize, client)
	framework.ExpectNoError(err, "While updating pvc for more size")
	gomega.Expect(pvclaim).NotTo(gomega.BeNil())

	pvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
	if pvcSize.Cmp(newSize) != 0 {
		framework.Failf("error updating pvc size %q", pvclaim.Name)
	}
	if guestCluster {
		ginkgo.By("Checking for PVC request size change on SVC PVC")
		b, err := verifyPvcRequestedSizeUpdateInSupervisorWithWait(svcPVCName, newSize)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(b).To(gomega.BeTrue())
	}

	ginkgo.By("Waiting for controller volume resize to finish")
	err = waitForPvResizeForGivenPvc(pvclaim, client, totalResizeWaitPeriod)
	framework.ExpectNoError(err, "While waiting for pvc resize to finish")

	if guestCluster {
		ginkgo.By("Checking for resize on SVC PV")
		verifyPVSizeinSupervisor(svcPVCName, newSize)
	}

	ginkgo.By("Checking for conditions on pvc")
	pvclaim, err = waitForPVCToReachFileSystemResizePendingCondition(client, namespace, pvclaim.Name, pollTimeout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	if guestCluster {
		ginkgo.By("Checking for 'FileSystemResizePending' status condition on SVC PVC")
		_, err = checkSvcPvcHasGivenStatusCondition(pv.Spec.CSI.VolumeHandle, true, v1.PersistentVolumeClaimFileSystemResizePending)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}

	ginkgo.By(fmt.Sprintf("Invoking QueryCNSVolumeWithResult with VolumeID: %s", volHandle))
	queryResult, err := e2eVSphere.queryCNSVolumeWithResult(volHandle)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	if len(queryResult.Volumes) == 0 {
		err = fmt.Errorf("QueryCNSVolumeWithResult returned no volume")
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	ginkgo.By("Verifying disk size requested in volume expansion is honored")
	newSizeInMb := int64(3072)
	if queryResult.Volumes[0].BackingObjectDetails.(*cnstypes.CnsBlockBackingDetails).CapacityInMb != newSizeInMb {
		err = fmt.Errorf("Got wrong disk size after volume expansion")
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// Create a new POD to use this PVC, and verify volume has been attached
	ginkgo.By("Creating a new pod to attach PV again to the node")
	pod, err = createPod(client, namespace, nil, []*v1.PersistentVolumeClaim{pvclaim}, false, execCommand)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By(fmt.Sprintf("Verify volume after expansion: %s is attached to the node: %s", pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName))
	vmUUID = getNodeUUID(client, pod.Spec.NodeName)
	if guestCluster {
		vmUUID, err = getVMUUIDFromNodeName(pod.Spec.NodeName)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}
	isDiskAttached, err = e2eVSphere.isVolumeAttachedToVM(client, volHandle, vmUUID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(isDiskAttached).To(gomega.BeTrue(), "Volume is not attached to the node")

	ginkgo.By("Verify after expansion the volume is accessible and filesystem type is as expected")
	_, err = framework.LookForStringInPodExec(namespace, pod.Name, []string{"/bin/cat", "/mnt/volume1/fstype"}, expectedContent, time.Minute)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("Waiting for file system resize to finish")
	pvclaim, err = waitForFSResize(pvclaim, client)
	framework.ExpectNoError(err, "while waiting for fs resize to finish")

	pvcConditions := pvclaim.Status.Conditions
	expectEqual(len(pvcConditions), 0, "pvc should not have conditions")

	ginkgo.By("Verify filesystem size for mount point /mnt/volume1 after expansion")
	fsSize, err := getFSSizeMb(f, pod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	// Filesystem size may be smaller than the size of the block volume.
	// Here since filesystem was already formatted on the original volume,
	// we can compare the new filesystem size with the original filesystem size.
	if fsSize < originalFsSize {
		framework.Failf("error updating filesystem size for %q. Resulting filesystem size is %d", pvclaim.Name, fsSize)
	}
	ginkgo.By("File system resize finished successfully")

	if guestCluster {
		ginkgo.By("Checking for PVC resize completion on SVC PVC")
		gomega.Expect(verifyResizeCompletedInSupervisor(svcPVCName)).To(gomega.BeTrue())
	}

	// Delete POD
	ginkgo.By(fmt.Sprintf("Deleting the new pod %s in namespace %s after expansion", pod.Name, namespace))
	err = fpod.DeletePodWithWait(client, pod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("Verify volume is detached from the node after expansion")
	isDiskDetached, err = e2eVSphere.waitForVolumeDetachedFromNode(client, pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(isDiskDetached).To(gomega.BeTrue(), fmt.Sprintf("Volume %q is not detached from the node %q", pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName))
}

func invokeTestForInvalidVolumeExpansion(f *framework.Framework, client clientset.Interface, namespace string, storagePolicyName string, profileID string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scParameters := make(map[string]string)
	scParameters[scParamFsType] = ext4FSType

	// Create Storage class and PVC
	ginkgo.By("Creating Storage Class and PVC with allowVolumeExpansion = false")
	var storageclass *storagev1.StorageClass
	var pvclaim *v1.PersistentVolumeClaim
	var err error

	if guestCluster {
		storagePolicyNameForSharedDatastores := GetAndExpectStringEnvVar(envStoragePolicyNameForSharedDatastores)
		scParameters[svStorageClassName] = storagePolicyNameForSharedDatastores
	}
	storageclass, pvclaim, err = createPVCAndStorageClass(client, namespace, nil, scParameters, "", nil, "", false, "")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	defer func() {
		err := client.StorageV1().StorageClasses().Delete(ctx, storageclass.Name, *metav1.NewDeleteOptions(0))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()
	defer func() {
		err := fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()

	// Waiting for PVC to be bound
	var pvclaims []*v1.PersistentVolumeClaim
	pvclaims = append(pvclaims, pvclaim)
	ginkgo.By("Waiting for all claims to be in bound state")
	_, err = fpv.WaitForPVClaimBoundPhase(client, pvclaims, framework.ClaimProvisionTimeout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// Modify PVC spec to trigger volume expansion
	// Expect this to fail
	ginkgo.By("Verify expanding pvc will fail because allowVolumeExpansion is false in StorageClass")
	currentPvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
	newSize := currentPvcSize.DeepCopy()
	newSize.Add(resource.MustParse("1Gi"))
	framework.Logf("currentPvcSize %v, newSize %v", currentPvcSize, newSize)
	_, err = expandPVCSize(pvclaim, newSize, client)
	gomega.Expect(err).To(gomega.HaveOccurred())
}

func invokeTestForInvalidOnlineVolumeExpansion(f *framework.Framework, client clientset.Interface, namespace string, expectedContent string, storagePolicyName string, profileID string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ginkgo.By("Invoking Test for Invalid Online Volume Expansion")
	scParameters := make(map[string]string)
	scParameters[scParamFsType] = ext4FSType
	// Create Storage class and PVC
	ginkgo.By("Creating Storage Class and PVC with allowVolumeExpansion is true")
	var storageclass *storagev1.StorageClass
	var pvclaim *v1.PersistentVolumeClaim
	var err error

	// Create a StorageClass that sets allowVolumeExpansion to true
	if guestCluster {
		storagePolicyNameForSharedDatastores := GetAndExpectStringEnvVar(envStoragePolicyNameForSharedDatastores)
		scParameters[svStorageClassName] = storagePolicyNameForSharedDatastores
	}
	storageclass, pvclaim, err = createPVCAndStorageClass(client, namespace, nil, scParameters, "", nil, "", true, "")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	defer func() {
		err := client.StorageV1().StorageClasses().Delete(ctx, storageclass.Name, *metav1.NewDeleteOptions(0))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()
	defer func() {
		err := fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()

	// Waiting for PVC to be bound
	var pvclaims []*v1.PersistentVolumeClaim
	pvclaims = append(pvclaims, pvclaim)
	ginkgo.By("Waiting for all claims to be in bound state")
	persistentvolumes, err := fpv.WaitForPVClaimBoundPhase(client, pvclaims, framework.ClaimProvisionTimeout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	pv := persistentvolumes[0]
	volHandle := pv.Spec.CSI.VolumeHandle
	svcPVCName := pv.Spec.CSI.VolumeHandle
	if guestCluster {
		volHandle = getVolumeIDFromSupervisorCluster(volHandle)
		gomega.Expect(volHandle).NotTo(gomega.BeEmpty())
	}

	defer func() {
		err := fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		err = e2eVSphere.waitForCNSVolumeToBeDeleted(pv.Spec.CSI.VolumeHandle)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()

	// Create a POD to use this PVC, and verify volume has been attached
	ginkgo.By("Creating pod to attach PV to the node")
	pod, err := createPod(client, namespace, nil, []*v1.PersistentVolumeClaim{pvclaim}, false, execCommand)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	var vmUUID string
	ginkgo.By(fmt.Sprintf("Verify volume: %s is attached to the node: %s", pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName))
	vmUUID = getNodeUUID(client, pod.Spec.NodeName)
	if guestCluster {
		vmUUID, err = getVMUUIDFromNodeName(pod.Spec.NodeName)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}
	isDiskAttached, err := e2eVSphere.isVolumeAttachedToVM(client, volHandle, vmUUID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(isDiskAttached).To(gomega.BeTrue(), "Volume is not attached to the node")

	ginkgo.By("Verify the volume is accessible and filesystem type is as expected")
	_, err = framework.LookForStringInPodExec(namespace, pod.Name, []string{"/bin/cat", "/mnt/volume1/fstype"}, expectedContent, time.Minute)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// Modify PVC spec to trigger volume expansion
	// We expand the PVC while no pod is using it to ensure offline expansion
	ginkgo.By("Verify expanding online pvc is not supported")
	currentPvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
	newSize := currentPvcSize.DeepCopy()
	newSize.Add(resource.MustParse("1Gi"))
	framework.Logf("currentPvcSize %v, newSize %v", currentPvcSize, newSize)
	pvclaim, err = expandPVCSize(pvclaim, newSize, client)
	framework.ExpectNoError(err, "While updating pvc for more size")
	gomega.Expect(pvclaim).NotTo(gomega.BeNil())

	pvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
	if pvcSize.Cmp(newSize) != 0 {
		framework.Failf("error updating pvc size %q", pvclaim.Name)
	}
	if guestCluster {
		ginkgo.By("Checking for PVC request size change on SVC PVC")
		_, err := verifyPvcRequestedSizeUpdateInSupervisorWithWait(svcPVCName, newSize)
		gomega.Expect(err).To(gomega.HaveOccurred())
	}

	ginkgo.By("Verify if controller resize failed")
	err = waitForPvResizeForGivenPvc(pvclaim, client, totalResizeWaitPeriod)
	gomega.Expect(err).To(gomega.HaveOccurred())

	// Delete POD
	ginkgo.By(fmt.Sprintf("Deleting the pod %s in namespace %s", pod.Name, namespace))
	err = fpod.DeletePodWithWait(client, pod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("Verify volume is detached from the node")
	isDiskDetached, err := e2eVSphere.waitForVolumeDetachedFromNode(client, pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(isDiskDetached).To(gomega.BeTrue(), fmt.Sprintf("Volume %q is not detached from the node %q", pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName))
}

func invokeTestForInvalidVolumeShrink(f *framework.Framework, client clientset.Interface, namespace string, storagePolicyName string, profileID string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scParameters := make(map[string]string)
	scParameters[scParamFsType] = ext4FSType

	// Create Storage class and PVC
	ginkgo.By("Creating Storage Class and PVC with allowVolumeExpansion = true")
	var storageclass *storagev1.StorageClass
	var pvclaim *v1.PersistentVolumeClaim
	var err error

	// Create a StorageClass that sets allowVolumeExpansion to true
	if guestCluster {
		storagePolicyNameForSharedDatastores := GetAndExpectStringEnvVar(envStoragePolicyNameForSharedDatastores)
		scParameters[svStorageClassName] = storagePolicyNameForSharedDatastores
	}
	storageclass, pvclaim, err = createPVCAndStorageClass(client, namespace, nil, scParameters, "", nil, "", true, "")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	defer func() {
		err := client.StorageV1().StorageClasses().Delete(ctx, storageclass.Name, *metav1.NewDeleteOptions(0))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()
	defer func() {
		err := fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()

	// Waiting for PVC to be bound
	var pvclaims []*v1.PersistentVolumeClaim
	pvclaims = append(pvclaims, pvclaim)
	ginkgo.By("Waiting for all claims to be in bound state")
	persistentvolumes, err := fpv.WaitForPVClaimBoundPhase(client, pvclaims, framework.ClaimProvisionTimeout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	pv := persistentvolumes[0]
	defer func() {
		err := fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		err = e2eVSphere.waitForCNSVolumeToBeDeleted(pv.Spec.CSI.VolumeHandle)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()

	// Modify PVC spec to a smaller size
	// Expect this to fail
	ginkgo.By("Verify operation will fail because volume shrinking is not supported")
	currentPvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
	newSize := currentPvcSize.DeepCopy()
	newSize.Sub(resource.MustParse("1Gi"))
	framework.Logf("currentPvcSize %v, newSize %v", currentPvcSize, newSize)
	_, err = expandPVCSize(pvclaim, newSize, client)
	gomega.Expect(err).To(gomega.HaveOccurred())
}

func invokeTestForInvalidVolumeExpansionStaticProvision(f *framework.Framework, client clientset.Interface, namespace string, storagePolicyName string, profileID string) {
	ginkgo.By("Invoking Test for Invalid Volume Expansion for Static Provisioning")

	var (
		fcdID               string
		pv                  *v1.PersistentVolume
		pvc                 *v1.PersistentVolumeClaim
		defaultDatacenter   *object.Datacenter
		defaultDatastore    *object.Datastore
		deleteFCDRequired   bool
		pandoraSyncWaitTime int
		err                 error
		datastoreURL        string
	)

	// Set up FCD
	if os.Getenv(envPandoraSyncWaitTime) != "" {
		pandoraSyncWaitTime, err = strconv.Atoi(os.Getenv(envPandoraSyncWaitTime))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	} else {
		pandoraSyncWaitTime = defaultPandoraSyncWaitTime
	}
	deleteFCDRequired = false
	var datacenters []string
	datastoreURL = GetAndExpectStringEnvVar(envSharedDatastoreURL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finder := find.NewFinder(e2eVSphere.Client.Client, false)
	cfg, err := getConfig()
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	dcList := strings.Split(cfg.Global.Datacenters, ",")
	for _, dc := range dcList {
		dcName := strings.TrimSpace(dc)
		if dcName != "" {
			datacenters = append(datacenters, dcName)
		}
	}

	for _, dc := range datacenters {
		defaultDatacenter, err = finder.Datacenter(ctx, dc)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		finder.SetDatacenter(defaultDatacenter)
		defaultDatastore, err = getDatastoreByURL(ctx, datastoreURL, defaultDatacenter)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}

	ginkgo.By("Creating FCD Disk")
	fcdID, err = e2eVSphere.createFCD(ctx, "BasicStaticFCD", diskSizeInMb, defaultDatastore.Reference())
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	deleteFCDRequired = true

	defer func() {
		if deleteFCDRequired && fcdID != "" && defaultDatastore != nil {
			ginkgo.By(fmt.Sprintf("Deleting FCD: %s", fcdID))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := e2eVSphere.deleteFCD(ctx, fcdID, defaultDatastore.Reference())
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}
	}()

	ginkgo.By(fmt.Sprintf("Sleeping for %v seconds to allow newly created FCD:%s to sync with pandora", pandoraSyncWaitTime, fcdID))
	time.Sleep(time.Duration(pandoraSyncWaitTime) * time.Second)

	// Creating label for PV.
	// PVC will use this label as Selector to find PV
	staticPVLabels := make(map[string]string)
	staticPVLabels["fcd-id"] = fcdID

	ginkgo.By("Creating the PV")
	pv = getPersistentVolumeSpec(fcdID, v1.PersistentVolumeReclaimDelete, staticPVLabels)
	pv, err = client.CoreV1().PersistentVolumes().Create(ctx, pv, metav1.CreateOptions{})
	if err != nil {
		return
	}

	defer func() {
		ginkgo.By("Verify PV should be deleted automatically")
		framework.ExpectNoError(framework.WaitForPersistentVolumeDeleted(client, pv.Name, poll, pollTimeout))
	}()

	err = e2eVSphere.waitForCNSVolumeToBeCreated(pv.Spec.CSI.VolumeHandle)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("Creating the PVC")
	pvc = getPersistentVolumeClaimSpec(namespace, staticPVLabels, pv.Name)
	pvc, err = client.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	defer func() {
		ginkgo.By("Deleting the PV Claim")
		framework.ExpectNoError(fpv.DeletePersistentVolumeClaim(client, pvc.Name, namespace), "Failed to delete PVC ", pvc.Name)
	}()

	// Wait for PV and PVC to Bind
	framework.ExpectNoError(fpv.WaitOnPVandPVC(client, namespace, pv, pvc))

	// Set deleteFCDRequired to false.
	// After PV, PVC is in the bind state, Deleting PVC should delete container volume.
	// So no need to delete FCD directly using vSphere API call.
	deleteFCDRequired = false

	ginkgo.By("Verifying CNS entry is present in cache")
	_, err = e2eVSphere.queryCNSVolumeWithResult(pv.Spec.CSI.VolumeHandle)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// Modify PVC spec to trigger volume expansion
	// Volume expansion will fail because it is not supported
	// on statically provisioned volume
	ginkgo.By("Verify operation will fail because volume expansion on statically provisioned volume is not supported")
	currentPvcSize := pvc.Spec.Resources.Requests[v1.ResourceStorage]
	newSize := currentPvcSize.DeepCopy()
	newSize.Add(resource.MustParse("1Gi"))
	framework.Logf("currentPvcSize %v, newSize %v", currentPvcSize, newSize)

	_, err = expandPVCSize(pvc, newSize, client)
	gomega.Expect(err).To(gomega.HaveOccurred())
}

func invokeTestForExpandVolumeMultipleTimes(f *framework.Framework, client clientset.Interface, namespace string, expectedContent string, storagePolicyName string, profileID string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ginkgo.By("Invoking Test to verify Multiple Volume Expansions on the same volume")
	scParameters := make(map[string]string)
	scParameters[scParamFsType] = ext4FSType
	// Create Storage class and PVC
	ginkgo.By("Creating Storage Class and PVC with allowVolumeExpansion = true")
	var storageclass *storagev1.StorageClass
	var pvclaim *v1.PersistentVolumeClaim
	var err error

	// Create a StorageClass that sets allowVolumeExpansion to true
	if guestCluster {
		storagePolicyNameForSharedDatastores := GetAndExpectStringEnvVar(envStoragePolicyNameForSharedDatastores)
		scParameters[svStorageClassName] = storagePolicyNameForSharedDatastores
	}
	storageclass, pvclaim, err = createPVCAndStorageClass(client, namespace, nil, scParameters, "", nil, "", true, "")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	defer func() {
		err := client.StorageV1().StorageClasses().Delete(ctx, storageclass.Name, *metav1.NewDeleteOptions(0))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()
	defer func() {
		err := fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()

	// Waiting for PVC to be bound
	var pvclaims []*v1.PersistentVolumeClaim
	pvclaims = append(pvclaims, pvclaim)
	ginkgo.By("Waiting for all claims to be in bound state")
	persistentvolumes, err := fpv.WaitForPVClaimBoundPhase(client, pvclaims, framework.ClaimProvisionTimeout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	pv := persistentvolumes[0]
	volHandle := pv.Spec.CSI.VolumeHandle
	svcPVCName := pv.Spec.CSI.VolumeHandle
	if guestCluster {
		volHandle = getVolumeIDFromSupervisorCluster(volHandle)
		gomega.Expect(volHandle).NotTo(gomega.BeEmpty())
	}

	defer func() {
		err := fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		err = e2eVSphere.waitForCNSVolumeToBeDeleted(pv.Spec.CSI.VolumeHandle)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()

	// Modify PVC spec to trigger volume expansion
	// We expand the PVC while no pod is using it to ensure offline expansion
	ginkgo.By("Expanding pvc 10 times")
	currentPvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
	newSize := currentPvcSize.DeepCopy()
	for i := 0; i < 10; i++ {
		newSize.Add(resource.MustParse("1Gi"))
		ginkgo.By(fmt.Sprintf("Expanding pvc to new size: %v", newSize))
		pvclaim, err = expandPVCSize(pvclaim, newSize, client)
		framework.ExpectNoError(err, "While updating pvc for more size")
		gomega.Expect(pvclaim).NotTo(gomega.BeNil())

		pvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
		if pvcSize.Cmp(newSize) != 0 {
			framework.Failf("error updating pvc %q to size %v", pvclaim.Name, newSize)
		}
		if guestCluster {
			ginkgo.By("Checking for PVC request size change on SVC PVC")
			b, err := verifyPvcRequestedSizeUpdateInSupervisorWithWait(svcPVCName, newSize)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(b).To(gomega.BeTrue())
		}
	}

	ginkgo.By("Waiting for controller resize to finish")
	err = waitForPvResizeForGivenPvc(pvclaim, client, totalResizeWaitPeriod)
	framework.ExpectNoError(err, "While waiting for pvc resize to finish")

	// reconciler takes a few secs in this case to update
	if guestCluster {
		ginkgo.By("Checking for resize on SVC PV")
		err = verifyPVSizeinSupervisorWithWait(svcPVCName, newSize)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}

	ginkgo.By("Checking for conditions on pvc")
	pvclaim, err = waitForPVCToReachFileSystemResizePendingCondition(client, namespace, pvclaim.Name, pollTimeout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	if guestCluster {
		ginkgo.By("Checking for 'FileSystemResizePending' status condition on SVC PVC")
		_, err = checkSvcPvcHasGivenStatusCondition(pv.Spec.CSI.VolumeHandle, true, v1.PersistentVolumeClaimFileSystemResizePending)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}

	ginkgo.By(fmt.Sprintf("Invoking QueryCNSVolumeWithResult with VolumeID: %s", volHandle))
	queryResult, err := e2eVSphere.queryCNSVolumeWithResult(volHandle)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	if len(queryResult.Volumes) == 0 {
		err = fmt.Errorf("QueryCNSVolumeWithResult returned no volume")
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	ginkgo.By("Verifying disk size requested in volume expansion is honored")
	newSizeInMb := int64(12288)
	if queryResult.Volumes[0].BackingObjectDetails.(*cnstypes.CnsBlockBackingDetails).CapacityInMb != newSizeInMb {
		err = fmt.Errorf("Received wrong disk size after volume expansion. Expected: %d Actual: %d", newSizeInMb, queryResult.Volumes[0].BackingObjectDetails.(*cnstypes.CnsBlockBackingDetails).CapacityInMb)
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// Create a POD to use this PVC, and verify volume has been attached
	ginkgo.By("Creating pod to attach PV to the node")
	pod, err := createPod(client, namespace, nil, []*v1.PersistentVolumeClaim{pvclaim}, false, execCommand)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	var vmUUID string
	ginkgo.By(fmt.Sprintf("Verify volume: %s is attached to the node: %s", pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName))
	vmUUID = getNodeUUID(client, pod.Spec.NodeName)
	if guestCluster {
		vmUUID, err = getVMUUIDFromNodeName(pod.Spec.NodeName)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}
	isDiskAttached, err := e2eVSphere.isVolumeAttachedToVM(client, volHandle, vmUUID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(isDiskAttached).To(gomega.BeTrue(), "Volume is not attached to the node")

	ginkgo.By("Verify the volume is accessible and filesystem type is as expected")
	_, err = framework.LookForStringInPodExec(namespace, pod.Name, []string{"/bin/cat", "/mnt/volume1/fstype"}, expectedContent, time.Minute)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("Waiting for file system resize to finish")
	pvclaim, err = waitForFSResize(pvclaim, client)
	framework.ExpectNoError(err, "while waiting for fs resize to finish")

	pvcConditions := pvclaim.Status.Conditions
	expectEqual(len(pvcConditions), 0, "pvc should not have conditions")

	ginkgo.By("Verify filesystem size for mount point /mnt/volume1")
	fsSize, err := getFSSizeMb(f, pod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	// Filesystem size may be smaller than the size of the block volume
	// so here we are checking if the new filesystem size is greater than
	// the original volume size as the filesystem is formatted for the
	// first time after pod creation
	gomega.Expect(fsSize).Should(gomega.BeNumerically(">", diskSizeInMb), fmt.Sprintf("error updating filesystem size for %q. Resulting filesystem size is %d", pvclaim.Name, fsSize))

	if guestCluster {
		ginkgo.By("Checking for PVC resize completion on SVC PVC")
		gomega.Expect(verifyResizeCompletedInSupervisor(svcPVCName)).To(gomega.BeTrue())
	}

	ginkgo.By(fmt.Sprintf("File system resize finished successfully to %d", fsSize))

	// Delete POD
	ginkgo.By(fmt.Sprintf("Deleting the pod %s in namespace %s", pod.Name, namespace))
	err = fpod.DeletePodWithWait(client, pod)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("Verify volume is detached from the node")
	isDiskDetached, err := e2eVSphere.waitForVolumeDetachedFromNode(client, pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(isDiskDetached).To(gomega.BeTrue(), fmt.Sprintf("Volume %q is not detached from the node %q", pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName))
}

func invokeTestForUnsupportedFileVolumeExpansion(f *framework.Framework, client clientset.Interface, namespace string, storagePolicyName string, profileID string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ginkgo.By("Invoking Test for Unsupported File Volume Expansion")
	scParameters := make(map[string]string)
	scParameters[scParamFsType] = nfs4FSType
	// Create Storage class and PVC
	ginkgo.By("Creating Storage Class and PVC with allowVolumeExpansion is true and filesystem type is nfs4FSType")
	var storageclass *storagev1.StorageClass
	var pvclaim *v1.PersistentVolumeClaim
	var err error

	// Create a StorageClass that sets allowVolumeExpansion to true
	storageclass, pvclaim, err = createPVCAndStorageClass(client, namespace, nil, scParameters, "", nil, "", true, v1.ReadWriteMany)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	defer func() {
		err := client.StorageV1().StorageClasses().Delete(ctx, storageclass.Name, *metav1.NewDeleteOptions(0))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()
	defer func() {
		err := fpv.DeletePersistentVolumeClaim(client, pvclaim.Name, namespace)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()

	// Waiting for PVC to be bound
	var pvclaims []*v1.PersistentVolumeClaim
	pvclaims = append(pvclaims, pvclaim)
	ginkgo.By("Waiting for all claims to be in bound state")
	//persistentvolumes
	_, err = fpv.WaitForPVClaimBoundPhase(client, pvclaims, framework.ClaimProvisionTimeout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// Modify PVC spec to trigger volume expansion
	// Expect to fail as file volume expansion is not supported
	ginkgo.By("Verify expanding file volume pvc is not supported")
	currentPvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
	newSize := currentPvcSize.DeepCopy()
	newSize.Add(resource.MustParse("1Gi"))
	framework.Logf("currentPvcSize %v, newSize %v", currentPvcSize, newSize)

	newPVC, err := expandPVCSize(pvclaim, newSize, client)
	framework.ExpectNoError(err, "While updating pvc for more size")
	pvclaim = newPVC
	gomega.Expect(pvclaim).NotTo(gomega.BeNil())

	pvcSize := pvclaim.Spec.Resources.Requests[v1.ResourceStorage]
	if pvcSize.Cmp(newSize) != 0 {
		framework.Failf("error updating pvc size %q", pvclaim.Name)
	}

	ginkgo.By("Verify if controller resize failed")
	err = waitForPvResizeForGivenPvc(pvclaim, client, totalResizeWaitPeriod)
	gomega.Expect(err).To(gomega.HaveOccurred())
}

// expandPVCSize expands PVC size
func expandPVCSize(origPVC *v1.PersistentVolumeClaim, size resource.Quantity, c clientset.Interface) (*v1.PersistentVolumeClaim, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pvcName := origPVC.Name
	updatedPVC := origPVC.DeepCopy()

	waitErr := wait.PollImmediate(resizePollInterval, 30*time.Second, func() (bool, error) {
		var err error
		updatedPVC, err = c.CoreV1().PersistentVolumeClaims(origPVC.Namespace).Get(ctx, pvcName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("error fetching pvc %q for resizing with %v", pvcName, err)
		}

		updatedPVC.Spec.Resources.Requests[v1.ResourceStorage] = size
		updatedPVC, err = c.CoreV1().PersistentVolumeClaims(origPVC.Namespace).Update(ctx, updatedPVC, metav1.UpdateOptions{})
		if err == nil {
			return true, nil
		}
		framework.Logf("Error updating pvc %s with %v", pvcName, err)
		return false, nil
	})
	return updatedPVC, waitErr
}

// waitForPvResizeForGivenPvc waits for the controller resize to be finished
func waitForPvResizeForGivenPvc(pvc *v1.PersistentVolumeClaim, c clientset.Interface, duration time.Duration) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pvName := pvc.Spec.VolumeName
	pvcSize := pvc.Spec.Resources.Requests[v1.ResourceStorage]
	pv, err := c.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return waitForPvResize(pv, c, pvcSize, duration)
}

// waitForPvResize waits for the controller resize to be finished
func waitForPvResize(pv *v1.PersistentVolume, c clientset.Interface, size resource.Quantity, duration time.Duration) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return wait.PollImmediate(resizePollInterval, duration, func() (bool, error) {
		pv, err := c.CoreV1().PersistentVolumes().Get(ctx, pv.Name, metav1.GetOptions{})

		if err != nil {
			return false, fmt.Errorf("error fetching pv %q for resizing %v", pv.Name, err)
		}

		pvSize := pv.Spec.Capacity[v1.ResourceStorage]

		// If pv size is greater or equal to requested size that means controller resize is finished.
		if pvSize.Cmp(size) >= 0 {
			return true, nil
		}
		return false, nil
	})
}

// waitForFSResize waits for the filesystem in the pv to be resized
func waitForFSResize(pvc *v1.PersistentVolumeClaim, c clientset.Interface) (*v1.PersistentVolumeClaim, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var updatedPVC *v1.PersistentVolumeClaim
	waitErr := wait.PollImmediate(resizePollInterval, totalResizeWaitPeriod, func() (bool, error) {
		var err error
		updatedPVC, err = c.CoreV1().PersistentVolumeClaims(pvc.Namespace).Get(ctx, pvc.Name, metav1.GetOptions{})

		if err != nil {
			return false, fmt.Errorf("error fetching pvc %q for checking for resize status : %v", pvc.Name, err)
		}

		pvcSize := updatedPVC.Spec.Resources.Requests[v1.ResourceStorage]
		pvcStatusSize := updatedPVC.Status.Capacity[v1.ResourceStorage]

		//If pvc's status field size is greater than or equal to pvc's size then done
		if pvcStatusSize.Cmp(pvcSize) >= 0 {
			return true, nil
		}
		return false, nil
	})
	return updatedPVC, waitErr
}

// getFSSizeMb returns filesystem size in Mb
func getFSSizeMb(f *framework.Framework, pod *v1.Pod) (int64, error) {
	output, err := storage_utils.PodExec(f, pod, "df -T -m | grep /mnt/volume1")
	if err != nil {
		return -1, fmt.Errorf("unable to find mount path via `df -T`: %v", err)
	}
	arrMountOut := strings.Fields(string(output))
	if len(arrMountOut) <= 0 {
		return -1, fmt.Errorf("error when parsing output of `df -T`. output: %s", string(output))
	}
	var devicePath, strSize string
	devicePath = arrMountOut[0]
	if devicePath == "" {
		return -1, fmt.Errorf("error when parsing output of `df -T` to find out devicePath of /mnt/volume1. output: %s", string(output))
	}
	strSize = arrMountOut[2]
	if strSize == "" {
		return -1, fmt.Errorf("error when parsing output of `df -T` to find out size of /mnt/volume1: output: %s", string(output))
	}

	intSizeInMb, err := strconv.ParseInt(strSize, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("failed to parse size %s into int size", strSize)
	}

	return intSizeInMb, nil
}

// expectEqual expects the specified two are the same, otherwise an exception raises
func expectEqual(actual interface{}, extra interface{}, explain ...interface{}) {
	gomega.ExpectWithOffset(1, actual).To(gomega.Equal(extra), explain...)
}
