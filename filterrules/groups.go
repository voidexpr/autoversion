package filterrules

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// GroupInfo is a built-in group, as defined in groups.json.
type GroupInfo struct {
	Name    string   `json:"name"`
	About   string   `json:"about"`
	Exclude []string `json:"exclude"` // rules as written, sugar included
	Use     []string `json:"use"`     // the groups it combines (default)
}

//go:embed groups.json
var groupsJSON []byte

var groupList, groupRules = loadGroups()

// loadGroups parses groups.json. Built-in Rules use nothing (shared spec), so
// a group that combines others gets their rules.
func loadGroups() ([]GroupInfo, map[string][]Rule) {
	var data struct {
		Groups []GroupInfo `json:"groups"`
	}
	if err := json.Unmarshal(groupsJSON, &data); err != nil {
		panic(fmt.Sprintf("groups.json: %v", err))
	}
	rules := map[string][]Rule{}
	for _, g := range data.Groups {
		var all []Rule
		for _, text := range g.Exclude {
			parsed, err := ParseRule(Exclude, text)
			if err != nil {
				panic(fmt.Sprintf("groups.json: %s: %v", g.Name, err))
			}
			for _, r := range parsed {
				r.Group = g.Name
				all = append(all, r)
			}
		}
		for _, name := range g.Use {
			used, ok := rules[name]
			if !ok {
				panic(fmt.Sprintf("groups.json: %s uses %s, not defined before it", g.Name, name))
			}
			all = append(all, used...)
		}
		rules[g.Name] = dedupe(all)
	}
	return data.Groups, rules
}

// Groups returns the built-in groups, in their order
func Groups() []GroupInfo {
	return slices.Clone(groupList)
}

// GroupNames returns the names of the built-in groups, in their order
func GroupNames() []string {
	var names []string
	for _, g := range groupList {
		names = append(names, g.Name)
	}
	return names
}

// Group returns the rules of a built-in group
func Group(name string) ([]Rule, error) {
	rules, ok := groupRules[name]
	if !ok {
		return nil, unknownGroup(name)
	}
	return slices.Clone(rules), nil
}

func unknownGroup(name string) error {
	return &Error{Code: "unknown-rules", Rule: name, Reason: "known groups: " + strings.Join(GroupNames(), ", ")}
}
