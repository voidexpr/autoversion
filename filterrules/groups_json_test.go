package filterrules

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// TestGroupsMatchJSON compares the group files with groups.json, the groups'
// previous form: same groups, text and rules, in the same order. It goes
// with the JSON.
func TestGroupsMatchJSON(t *testing.T) {
	raw, err := os.ReadFile("groups.json")
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Groups []struct {
			Name    string   `json:"name"`
			About   string   `json:"about"`
			Exclude []string `json:"exclude"`
			Use     []string `json:"use"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}

	files := map[string]GroupInfo{}
	for _, g := range Groups() {
		files[g.Name] = g
	}
	if len(files) != len(data.Groups) {
		t.Errorf("%d group files, %d in the JSON", len(files), len(data.Groups))
	}
	jsonRules := map[string][]Rule{}
	for _, g := range data.Groups {
		file, ok := files[g.Name]
		if !ok {
			t.Errorf("%s: no file", g.Name)
			continue
		}
		if g.Name != "default" && file.About != g.About {
			t.Errorf("%s: about %q, JSON %q", g.Name, file.About, g.About)
		}
		if !slices.Equal(file.Exclude, g.Exclude) || !slices.Equal(file.Use, g.Use) {
			t.Errorf("%s: lines %q use %q, JSON %q use %q", g.Name, file.Exclude, file.Use, g.Exclude, g.Use)
		}

		var all []Rule
		for _, text := range g.Exclude {
			parsed, err := ParseRule(Exclude, text)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range parsed {
				r.Group = g.Name
				all = append(all, r)
			}
		}
		for _, name := range g.Use {
			all = append(all, jsonRules[name]...)
		}
		jsonRules[g.Name] = dedupe(all)
		got, err := Group(g.Name)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ruleStrings(got), ruleStrings(jsonRules[g.Name])) {
			t.Errorf("%s: rules\n got %q\nJSON %q", g.Name, ruleStrings(got), ruleStrings(jsonRules[g.Name]))
		}
		for i := range got {
			if got[i].Group != jsonRules[g.Name][i].Group {
				t.Errorf("%s: rule %d from group %q, JSON %q", g.Name, i, got[i].Group, jsonRules[g.Name][i].Group)
			}
		}
	}
}
