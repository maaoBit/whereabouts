package reconciler

import (
	"context"
	"fmt"

	"github.com/dougbtv/whereabouts/pkg/allocate"
	"github.com/dougbtv/whereabouts/pkg/logging"
	"github.com/dougbtv/whereabouts/pkg/storage"
	"github.com/dougbtv/whereabouts/pkg/storage/kubernetes"
	"github.com/dougbtv/whereabouts/pkg/types"
)

type ReconcileLooper struct {
	ctx         context.Context
	k8sClient   kubernetes.Client
	livePodRefs []string
	orphanedIPs []OrphanedIPReservations
}

type OrphanedIPReservations struct {
	Pool        storage.IPPool
	Allocations []types.IPReservation
}

func NewReconcileLooper(kubeConfigPath string, ctx context.Context) (*ReconcileLooper, error) {
	logging.Debugf("NewReconcileLooper - Kubernetes config file located at: %s", kubeConfigPath)
	k8sClient, err := kubernetes.NewClient(kubeConfigPath)
	if err != nil {
		return nil, logging.Errorf("failed to instantiate the Kubernetes client: %+v", err)
	}
	logging.Debugf("successfully read the kubernetes configuration file located at: %s", kubeConfigPath)

	podRefs, err := getPodRefs(*k8sClient)
	if err != nil {
		return nil, err
	}

	looper := &ReconcileLooper{
		ctx:         ctx,
		k8sClient:   *k8sClient,
		livePodRefs: podRefs,
	}

	if err := looper.findOrphanedIPsPerPool(); err != nil {
		return nil, err
	}
	return looper, nil
}

func getPodRefs(k8sClient kubernetes.Client) ([]string, error) {
	pods, err := k8sClient.ListPods()
	if err != nil {
		return nil, err
	}

	var podRefs []string
	for _, pod := range pods {
		podRefs = append(podRefs, fmt.Sprintf("%s/%s", pod.GetNamespace(), pod.GetName()))
	}
	return podRefs, err
}

func (rl *ReconcileLooper) findOrphanedIPsPerPool() error {
	ipPools, err := rl.k8sClient.ListIPPools(rl.ctx)
	if err != nil {
		return logging.Errorf("failed to retrieve all IP pools: %v", err)
	}

	for _, pool := range ipPools {
		orphanIP := OrphanedIPReservations{
			Pool: pool,
		}
		for _, allocation := range pool.Allocations() {
			logging.Debugf("the IP reservation: %s", allocation)
			if allocation.PodRef == "" {
				_ = logging.Errorf("pod ref missing for Allocations: %s", allocation)
				continue
			}
			if !rl.isPodAlive(allocation.PodRef) {
				logging.Debugf("pod ref %s is not listed in the live pods list", allocation.PodRef)
				orphanIP.Allocations = append(orphanIP.Allocations, allocation)
			}
		}
		if len(orphanIP.Allocations) > 0 {
			rl.orphanedIPs = append(rl.orphanedIPs, orphanIP)
		}
	}

	return nil
}

func (rl ReconcileLooper) isPodAlive(podRef string) bool {
	for _, livePodRef := range rl.livePodRefs {
		if podRef == livePodRef {
			return true
		}
	}
	return false
}

func (rl ReconcileLooper) ReconcileIPPools() ([]types.IPReservation, error) {
	matchByPodRef := func(reservations []types.IPReservation, podRef string) int {
		foundidx := -1
		for idx, v := range reservations {
			if v.PodRef == podRef {
				return idx
			}
		}
		return foundidx
	}

	var err error
	var totalCleanedUpIps []types.IPReservation
	for _, orphanedIP := range rl.orphanedIPs {
		originalIPReservations := orphanedIP.Pool.Allocations()
		currentIPReservations := orphanedIP.Pool.Allocations()
		podRefsToDeallocate := findOutPodRefsToDeallocateIPsFrom(orphanedIP)
		for _, podRef := range podRefsToDeallocate {
			currentIPReservations, _, err = allocate.IterateForDeallocation(currentIPReservations, podRef, matchByPodRef)
			if err != nil {
				return nil, err
			}
		}

		logging.Debugf("Going to update the reserve list to: %+v", currentIPReservations)
		if err := orphanedIP.Pool.Update(rl.ctx, currentIPReservations); err != nil {
			return nil, logging.Errorf("failed to update the reservation list: %v", err)
		}
		totalCleanedUpIps = append(totalCleanedUpIps, computeCleanedUpIPs(originalIPReservations, currentIPReservations)...)
	}

	return totalCleanedUpIps, nil
}

func computeCleanedUpIPs(oldIPReservations []types.IPReservation, newIPReservations []types.IPReservation) []types.IPReservation {
	var deletedReservations []types.IPReservation
	ledger := make(map[string]bool)
	for _, reservation := range newIPReservations {
		ledger[reservation.IP.String()] = true
	}
	for _, reservation := range oldIPReservations {
		if _, found := ledger[reservation.IP.String()]; !found {
			deletedReservations = append(deletedReservations, reservation)
		}
	}
	return deletedReservations
}

func findOutPodRefsToDeallocateIPsFrom(orphanedIP OrphanedIPReservations) []string {
	var podRefsToDeallocate []string
	for _, orphanedAllocation := range orphanedIP.Allocations {
		podRefsToDeallocate = append(podRefsToDeallocate, orphanedAllocation.PodRef)
	}
	return podRefsToDeallocate
}
