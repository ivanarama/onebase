package reviewrouting

import (
	"fmt"
	"regexp"
	"sort"
	"time"
)

const leaseTTL = 30 * time.Minute
const renewWindow = 5 * time.Minute

var timestampPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,6})?Z$`)

type Record struct {
	ID        string
	CreatedAt string
	Payload   map[string]any
}

func serverTime(text string) (time.Time, error) {
	if !timestampPattern.MatchString(text) {
		return time.Time{}, fmt.Errorf("routing timestamp must be exact UTC server time")
	}
	t, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || t.Year() < 1 {
		return time.Time{}, fmt.Errorf("invalid routing server timestamp")
	}
	return t, nil
}

func (record Record) validate() error {
	if !idPattern.MatchString(record.ID) {
		return fmt.Errorf("routing server ID must be bounded positive decimal text")
	}
	if _, err := serverTime(record.CreatedAt); err != nil {
		return err
	}
	return Validate(record.Payload)
}

func before(left, right Record) bool {
	a, _ := serverTime(left.CreatedAt)
	b, _ := serverTime(right.CreatedAt)
	if !a.Equal(b) {
		return a.Before(b)
	}
	if len(left.ID) != len(right.ID) {
		return len(left.ID) < len(right.ID)
	}
	return left.ID < right.ID
}

type leaseNode struct {
	Record
	root, previous string
}

type leaseView struct{ root, active leaseNode }

func leaseNodes(records []Record, root Record, purpose string) ([]leaseNode, error) {
	context, err := Context(root.Payload)
	if err != nil {
		return nil, err
	}
	nodes := []leaseNode{{Record: root}}
	for _, record := range records {
		data := record.Payload
		if data["kind"] != "lease" || data["root"] != root.ID {
			continue
		}
		if data["context_sha256"] != context || data["purpose"] != purpose {
			return nil, fmt.Errorf("lease does not bind its immutable root")
		}
		nodes = append(nodes, leaseNode{record, root.ID, data["previous"].(string)})
	}
	return nodes, nil
}

func eligibleLease(child, parent leaseNode) bool {
	if !before(parent.Record, child.Record) {
		return false
	}
	parentTime, _ := serverTime(parent.CreatedAt)
	childTime, _ := serverTime(child.CreatedAt)
	expiry := parentTime.Add(leaseTTL)
	if childTime.Before(expiry) {
		return child.Payload["owner"] == parent.Payload["owner"] && expiry.Sub(childTime) < renewWindow
	}
	return child.Payload["owner"] != parent.Payload["owner"]
}

func resolveLease(nodes []leaseNode, now time.Time) (leaseView, error) {
	var result leaseView
	if len(nodes) == 0 || len(nodes) > 4096 {
		return result, fmt.Errorf("lease history is missing or oversized")
	}
	byID := map[string]leaseNode{}
	roots := []leaseNode{}
	for _, node := range nodes {
		if _, found := byID[node.ID]; found {
			return result, fmt.Errorf("duplicate lease server ID")
		}
		byID[node.ID] = node
		if node.root == "" {
			roots = append(roots, node)
		}
	}
	if len(roots) == 0 {
		return result, fmt.Errorf("lease root is missing")
	}
	sort.Slice(roots, func(i, j int) bool { return before(roots[i].Record, roots[j].Record) })
	rootIDs := map[string]bool{}
	for _, root := range roots {
		rootIDs[root.ID] = true
	}
	children := map[string][]leaseNode{}
	for _, node := range nodes {
		if node.root == "" {
			continue
		}
		parent, ok := byID[node.previous]
		if !ok || !rootIDs[node.root] {
			return result, fmt.Errorf("lease references missing history")
		}
		if parent.root == "" && parent.ID != node.root || parent.root != "" && parent.root != node.root {
			return result, fmt.Errorf("lease crosses root ownership")
		}
		children[node.previous] = append(children[node.previous], node)
	}
	finished := map[string]bool{}
	for id := range rootIDs {
		finished[id] = true
	}
	for _, node := range nodes {
		seen := map[string]bool{}
		cursor := node
		for !finished[cursor.ID] {
			if seen[cursor.ID] {
				return result, fmt.Errorf("lease history contains a cycle")
			}
			seen[cursor.ID] = true
			cursor = byID[cursor.previous]
		}
		for id := range seen {
			finished[id] = true
		}
	}
	active := roots[0]
	for {
		var eligible []leaseNode
		for _, child := range children[active.ID] {
			if child.root == roots[0].ID && eligibleLease(child, active) {
				eligible = append(eligible, child)
			}
		}
		if len(eligible) == 0 {
			break
		}
		sort.Slice(eligible, func(i, j int) bool { return before(eligible[i].Record, eligible[j].Record) })
		active = eligible[0]
	}
	activeTime, _ := serverTime(active.CreatedAt)
	if activeTime.After(now) {
		return result, fmt.Errorf("lease server time is in the future")
	}
	return leaseView{roots[0], active}, nil
}

func ownerAt(records []Record, root, receipt Record, purpose string) error {
	var prior []Record
	for _, item := range records {
		if before(item, receipt) {
			prior = append(prior, item)
		}
	}
	nodes, err := leaseNodes(prior, root, purpose)
	if err != nil {
		return err
	}
	now, err := serverTime(receipt.CreatedAt)
	if err != nil {
		return err
	}
	view, err := resolveLease(nodes, now)
	if err != nil {
		return err
	}
	activeTime, _ := serverTime(view.active.CreatedAt)
	if receipt.Payload["lease_id"] != view.active.ID || receipt.Payload["owner"] != view.active.Payload["owner"] || now.Before(activeTime) || !now.Before(activeTime.Add(leaseTTL)) {
		return fmt.Errorf("receipt was not issued by the active returned-ID/UUID owner")
	}
	return nil
}
