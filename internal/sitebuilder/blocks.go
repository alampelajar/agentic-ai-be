package sitebuilder

import (
	"encoding/json"
	"fmt"
	"slices"
)

// Blocks is how a look shapes the template's finished sections (web/src/blocks/). The
// worker writes it into web/src/look.ts; the sections read it, so the agent only brings
// the content and the order of the sections. The field names match the Look type in
// web/src/blocks/types.ts.
type Blocks struct {
	Header    string `json:"header"`
	Hero      string `json:"hero"`
	Event     string `json:"event"`
	List      string `json:"list"`
	Steps     string `json:"steps"`
	Statement string `json:"statement"`
	Footer    string `json:"footer"`
	Button    string `json:"button"`
	Frame     string `json:"frame"`
	Caps      bool   `json:"caps"`
	Centered  bool   `json:"centered"`
	Rule      bool   `json:"rule"`
	Tilt      bool   `json:"tilt"`
	Airy      bool   `json:"airy"`
}

// blockChoices are the variants each section has, as in web/src/blocks/types.ts.
var blockChoices = map[string][]string{
	"header":    {"split", "centered", "bar", "announce", "topbar"},
	"hero":      {"editorial", "calm", "split", "poster", "pop", "panel", "framed"},
	"event":     {"arch", "band", "plain"},
	"list":      {"rows", "cards", "boxes", "bento", "columns", "tiles"},
	"steps":     {"row", "timeline"},
	"statement": {"quote", "band"},
	"footer":    {"columns", "centered", "dark", "row"},
	"button":    {"square", "soft", "pill", "link"},
	"frame":     {"line", "bold", "dashed", "double", "none"},
}

func (b Blocks) validate() error {
	for field, value := range map[string]string{
		"header": b.Header, "hero": b.Hero, "event": b.Event, "list": b.List, "steps": b.Steps,
		"statement": b.Statement, "footer": b.Footer, "button": b.Button, "frame": b.Frame,
	} {
		if !slices.Contains(blockChoices[field], value) {
			return fmt.Errorf("blocks.%s %q is not one of %v", field, value, blockChoices[field])
		}
	}
	return nil
}

// LookTS is web/src/look.ts for a look.
func LookTS(d Direction) string {
	data, _ := json.MarshalIndent(d.Blocks, "", "  ")
	return "// Written by the builder from the look \"" + d.Key + "\". The agent cannot change it.\n" +
		"import type { Look } from './blocks/types'\n\nexport const look: Look = " + string(data) + "\n"
}
