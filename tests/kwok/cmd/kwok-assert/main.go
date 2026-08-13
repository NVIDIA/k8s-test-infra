// Copyright (c) 2026, NVIDIA CORPORATION. All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/materialize"
)

const (
	ownerLabel           = "tests.mokka.nvidia.com/kwok-cluster"
	eligibleLabel        = "mokka.nvidia.com/sgpu-node"
	assignedLabel        = "mokka.nvidia.com/sgpu-assigned"
	assignmentAnnotation = "mokka.nvidia.com/sgpu-assignment"
)

type options struct {
	state             string
	inventoryPath     string
	racksPath         string
	nodesPath         string
	clusterLabel      string
	expectedRacks     int
	nodesPerRack      int
	expectedNodes     int
	expectedEligible  int
	expectedAllocated int
	requestsSatisfied bool
}

type result struct {
	SchemaVersion    int      `json:"schemaVersion"`
	State            string   `json:"state"`
	Success          bool     `json:"success"`
	AssignmentDigest string   `json:"assignmentDigest,omitempty"`
	Racks            int      `json:"racks"`
	Nodes            int      `json:"nodes"`
	EligibleNodes    int      `json:"eligibleNodes"`
	AllocatedNodes   int      `json:"allocatedNodes"`
	ProjectedNodes   int      `json:"projectedNodes"`
	Errors           []string `json:"errors,omitempty"`
}

type assignment struct {
	Version   int             `json:"v"`
	Inventory objectReference `json:"inventory"`
	Rack      objectReference `json:"rack"`
	RackGroup string          `json:"rackGroup"`
	RackIndex int32           `json:"rackIndex"`
	SlotIndex int32           `json:"slotIndex"`
	NodeUID   types.UID       `json:"nodeUID"`
}

type objectReference struct {
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
}

type binding struct {
	nodeName  string
	nodeUID   types.UID
	rackName  string
	rackUID   types.UID
	rackIndex int32
	slotIndex int32
}

func main() {
	var o options
	flag.StringVar(&o.state, "state", "unknown", "state name recorded in the result")
	flag.StringVar(&o.inventoryPath, "inventory", "", "SGPUInventory JSON file")
	flag.StringVar(&o.racksPath, "racks", "", "SGPURackList JSON file")
	flag.StringVar(&o.nodesPath, "nodes", "", "NodeList JSON file")
	flag.StringVar(&o.clusterLabel, "cluster-label", "", "expected Node ownership-label value")
	flag.IntVar(&o.expectedRacks, "expected-racks", -1, "expected rack count")
	flag.IntVar(&o.nodesPerRack, "nodes-per-rack", -1, "expected slots per rack")
	flag.IntVar(&o.expectedNodes, "expected-nodes", -1, "expected owned Node count")
	flag.IntVar(&o.expectedEligible, "expected-eligible", -1, "expected eligible Node count")
	flag.IntVar(&o.expectedAllocated, "expected-allocated", -1, "expected allocated/projected count")
	flag.BoolVar(&o.requestsSatisfied, "requests-satisfied", false, "expect RequestsSatisfied=True")
	flag.Parse()

	checked, err := checkFiles(o)
	if err != nil {
		checked = result{SchemaVersion: 1, State: o.state, Errors: []string{err.Error()}}
	}
	checked.Success = len(checked.Errors) == 0
	if encodeErr := json.NewEncoder(os.Stdout).Encode(checked); encodeErr != nil {
		fmt.Fprintf(os.Stderr, "encode result: %v\n", encodeErr)
		os.Exit(2)
	}
	if !checked.Success {
		os.Exit(1)
	}
}

func checkFiles(o options) (result, error) {
	if o.inventoryPath == "" || o.racksPath == "" || o.nodesPath == "" || o.clusterLabel == "" {
		return result{}, errors.New("inventory, racks, nodes, and cluster-label are required")
	}
	if o.expectedRacks < 0 || o.nodesPerRack < 1 || o.expectedNodes < 0 ||
		o.expectedEligible < 0 || o.expectedAllocated < 0 {
		return result{}, errors.New("expected counts must be non-negative and nodes-per-rack positive")
	}
	var inventory mokkav1alpha1.SGPUInventory
	var racks mokkav1alpha1.SGPURackList
	var nodes corev1.NodeList
	if err := readJSON(o.inventoryPath, &inventory); err != nil {
		return result{}, err
	}
	if err := readJSON(o.racksPath, &racks); err != nil {
		return result{}, err
	}
	if err := readJSON(o.nodesPath, &nodes); err != nil {
		return result{}, err
	}
	return check(o, &inventory, &racks, &nodes), nil
}

func readJSON(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("decode %q: %w", path, err)
	}
	return nil
}

func check(o options, inventory *mokkav1alpha1.SGPUInventory, racks *mokkav1alpha1.SGPURackList, nodes *corev1.NodeList) result {
	checked := result{SchemaVersion: 1, State: o.state, Racks: len(racks.Items), Nodes: len(nodes.Items)}
	addError := func(format string, args ...any) {
		checked.Errors = append(checked.Errors, fmt.Sprintf(format, args...))
	}

	capacity := int64(o.expectedRacks * o.nodesPerRack)
	pending := int64(o.expectedEligible - o.expectedAllocated)
	if inventory.Name != "mokka-kwok" || inventory.UID == "" {
		addError("unexpected inventory identity %q/%q", inventory.Name, inventory.UID)
	}
	if inventory.Status.ObservedGeneration != inventory.Generation {
		addError("inventory generation %d is not observed (%d)", inventory.Generation, inventory.Status.ObservedGeneration)
	}
	if inventory.Status.Capacity.Racks != int64(o.expectedRacks) ||
		inventory.Status.Capacity.NodeSlots != capacity || inventory.Status.Capacity.GPUs != capacity {
		addError("inventory capacity is %+v, want racks=%d nodeSlots=%d GPUs=%d",
			inventory.Status.Capacity, o.expectedRacks, capacity, capacity)
	}
	wantAvailable := capacity - int64(o.expectedAllocated)
	usage := inventory.Status.Usage
	if usage.RequestedNodes != int64(o.expectedEligible) ||
		usage.AllocatedNodes != int64(o.expectedAllocated) ||
		usage.ProjectedNodes != int64(o.expectedAllocated) ||
		usage.AvailableNodes != wantAvailable || usage.PendingNodes != pending || usage.ConflictingNodes != 0 {
		addError("inventory usage is %+v, want requested=%d allocated/projected=%d available=%d pending=%d conflicts=0",
			usage, o.expectedEligible, o.expectedAllocated, wantAvailable, pending)
	}
	for _, conditionType := range []string{"Accepted", "ResolvedRefs", "Materialized", "NodesProjected"} {
		checkCondition(addError, inventory.Status.Conditions, conditionType, metav1.ConditionTrue)
	}
	wantRequests := metav1.ConditionFalse
	if o.requestsSatisfied {
		wantRequests = metav1.ConditionTrue
	}
	checkCondition(addError, inventory.Status.Conditions, "RequestsSatisfied", wantRequests)

	bindings := make(map[types.UID]binding, o.expectedAllocated)
	seenIndexes := make(map[int32]struct{}, o.expectedRacks)
	boundCount := 0
	for i := range racks.Items {
		rack := &racks.Items[i]
		index := rack.Spec.Identity.RackIndex
		if _, duplicate := seenIndexes[index]; duplicate {
			addError("rack index %d is duplicated", index)
		}
		seenIndexes[index] = struct{}{}
		wantName := materialize.RackName(inventory.Name, inventory.UID, "compute", index)
		if rack.Name != wantName {
			addError("rack index %d is named %q, want deterministic name %q", index, rack.Name, wantName)
		}
		if index < 0 || int(index) >= o.expectedRacks || rack.Spec.Identity.RackGroup != "compute" {
			addError("rack %q has invalid coordinate %q/%d", rack.Name, rack.Spec.Identity.RackGroup, index)
		}
		if rack.Spec.InventoryRef.Name != inventory.Name || rack.Spec.InventoryRef.UID != inventory.UID {
			addError("rack %q has wrong inventory reference", rack.Name)
		}
		if len(rack.Spec.Slots) != o.nodesPerRack {
			addError("rack %q has %d slots, want %d", rack.Name, len(rack.Spec.Slots), o.nodesPerRack)
		}
		assignedInRack := 0
		for slotIndex := range rack.Spec.Slots {
			slot := &rack.Spec.Slots[slotIndex]
			if slot.Index != int32(slotIndex) {
				addError("rack %q slot position %d has index %d", rack.Name, slotIndex, slot.Index)
			}
			if slot.NodeRef == nil {
				continue
			}
			assignedInRack++
			boundCount++
			if _, duplicate := bindings[slot.NodeRef.UID]; duplicate {
				addError("Node UID %q is bound more than once", slot.NodeRef.UID)
			}
			bindings[slot.NodeRef.UID] = binding{
				nodeName: slot.NodeRef.Name, nodeUID: slot.NodeRef.UID,
				rackName: rack.Name, rackUID: rack.UID, rackIndex: index, slotIndex: slot.Index,
			}
		}
		if rack.Status.ObservedGeneration != rack.Generation || int(rack.Status.AssignedSlots) != assignedInRack ||
			int(rack.Status.ProjectedSlots) != assignedInRack {
			addError("rack %q status is stale or incomplete: %+v", rack.Name, rack.Status)
		}
		checkCondition(addError, rack.Status.Conditions, "Ready", metav1.ConditionTrue)
		checkCondition(addError, rack.Status.Conditions, "NodesProjected", metav1.ConditionTrue)
	}
	if len(racks.Items) != o.expectedRacks || len(seenIndexes) != o.expectedRacks {
		addError("observed %d racks and %d unique indexes, want %d", len(racks.Items), len(seenIndexes), o.expectedRacks)
	}
	if boundCount != o.expectedAllocated {
		addError("observed %d rack bindings, want %d", boundCount, o.expectedAllocated)
	}

	digestRecords := make([]string, 0, o.expectedAllocated)
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if node.Labels[ownerLabel] != o.clusterLabel {
			addError("Node %q lacks exact ownership label", node.Name)
		}
		if node.Labels[eligibleLabel] == "true" {
			checked.EligibleNodes++
		}
		encoded := node.Annotations[assignmentAnnotation]
		assigned := node.Labels[assignedLabel] == "true"
		if encoded == "" && !assigned {
			continue
		}
		if encoded == "" || !assigned {
			addError("Node %q has only part of the assignment projection", node.Name)
			continue
		}
		checked.ProjectedNodes++
		var projected assignment
		if err := json.Unmarshal([]byte(encoded), &projected); err != nil {
			addError("Node %q has invalid assignment JSON: %v", node.Name, err)
			continue
		}
		want, found := bindings[node.UID]
		if !found || want.nodeName != node.Name {
			addError("Node %q/%q is projected without an exact rack binding", node.Name, node.UID)
			continue
		}
		if projected.Version != 1 || projected.Inventory.Name != inventory.Name ||
			projected.Inventory.UID != inventory.UID || projected.Rack.Name != want.rackName ||
			projected.Rack.UID != want.rackUID || projected.RackGroup != "compute" ||
			projected.RackIndex != want.rackIndex || projected.SlotIndex != want.slotIndex ||
			projected.NodeUID != node.UID {
			addError("Node %q projection does not match its exact rack binding", node.Name)
		}
		digestRecords = append(digestRecords, node.Name+"\x00"+string(node.UID)+"\x00"+encoded)
	}
	checked.AllocatedNodes = len(bindings)
	if len(nodes.Items) != o.expectedNodes || checked.EligibleNodes != o.expectedEligible ||
		checked.ProjectedNodes != o.expectedAllocated {
		addError("observed nodes=%d eligible=%d projected=%d, want %d/%d/%d",
			len(nodes.Items), checked.EligibleNodes, checked.ProjectedNodes,
			o.expectedNodes, o.expectedEligible, o.expectedAllocated)
	}
	slices.Sort(digestRecords)
	hash := sha256.New()
	for _, record := range digestRecords {
		_, _ = hash.Write([]byte(record))
		_, _ = hash.Write([]byte{'\n'})
	}
	checked.AssignmentDigest = hex.EncodeToString(hash.Sum(nil))
	checked.Success = len(checked.Errors) == 0
	return checked
}

func checkCondition(addError func(string, ...any), conditions []metav1.Condition, conditionType string, want metav1.ConditionStatus) {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			if condition.Status != want {
				addError("condition %s is %s, want %s", conditionType, condition.Status, want)
			}
			return
		}
	}
	addError("condition %s is missing", conditionType)
}
