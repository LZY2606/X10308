package sourcemodel

import (
	"encoding/json"
	"fmt"
	"strings"
)

// netJSON is the on-disk JSON shape of a petrinet source document.
type netJSON struct {
	Schema     string `json:"schema"`
	ID         string `json:"id"`
	Revision   string `json:"revision"`
	Title      string `json:"title"`
	Places     []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Tokens   string `json:"tokens"`
		Capacity string `json:"capacity"`
		X        string `json:"x"`
		Y        string `json:"y"`
	} `json:"places"`
	Transitions []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		X    string `json:"x"`
		Y    string `json:"y"`
	} `json:"transitions"`
	Arcs []struct {
		ID     string `json:"id"`
		From   string `json:"from"`
		To     string `json:"to"`
		Weight string `json:"weight"`
		Kind   string `json:"kind"`
	} `json:"arcs"`
}

// strategyJSON is the on-disk JSON shape of a strategy source document.
type strategyJSON struct {
	Schema   string `json:"schema"`
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Title    string `json:"title"`
	Kind     string `json:"kind"`
	Budget   string `json:"budget"`
	Frontier string `json:"frontier"`
	Targets  []struct {
		ID     string            `json:"id"`
		Tokens map[string]string `json:"tokens"`
	} `json:"targets"`
}

// ParseNet decodes raw bytes into a source document without normalizing
// any quantity: every token count, capacity, weight and coordinate keeps
// its original text plus unit/basis annotation.
func ParseNet(raw []byte) (*Doc, error) {
	var nj netJSON
	if err := json.Unmarshal(raw, &nj); err != nil {
		return nil, fmt.Errorf("net json: %w", err)
	}
	if nj.ID == "" {
		return nil, fmt.Errorf("net document missing id")
	}
	doc := &Doc{ID: nj.ID, Kind: "petrinet", Revision: nj.Revision, Title: nj.Title, RawBytes: append([]byte(nil), raw...)}
	doc.Payload = map[string]interface{}{"schema": nj.Schema}
	for _, p := range nj.Places {
		n := Node{ID: p.ID, Kind: "place", Name: p.Name}
		if p.Tokens != "" {
			n.Tokens = &Raw{Text: p.Tokens, Unit: "token"}
		}
		if p.Capacity != "" {
			n.Capacity = &Raw{Text: p.Capacity, Unit: "token"}
		}
		if p.X != "" {
			n.X = &Raw{Text: p.X, Unit: "px", Basis: "cartesian-px"}
		}
		if p.Y != "" {
			n.Y = &Raw{Text: p.Y, Unit: "px", Basis: "cartesian-px"}
		}
		doc.Nodes = append(doc.Nodes, n)
	}
	for _, t := range nj.Transitions {
		n := Node{ID: t.ID, Kind: "transition", Name: t.Name}
		if t.X != "" {
			n.X = &Raw{Text: t.X, Unit: "px", Basis: "cartesian-px"}
		}
		if t.Y != "" {
			n.Y = &Raw{Text: t.Y, Unit: "px", Basis: "cartesian-px"}
		}
		doc.Nodes = append(doc.Nodes, n)
	}
	for _, a := range nj.Arcs {
		kind := a.Kind
		if kind == "" {
			kind = "normal"
		}
		arc := Arc{ID: a.ID, From: a.From, To: a.To, Kind: kind}
		if a.Weight != "" {
			arc.Weight = &Raw{Text: a.Weight, Unit: "token"}
		}
		doc.Arcs = append(doc.Arcs, arc)
	}
	return doc, nil
}

// ParseStrategy decodes a strategy/budget source document.
func ParseStrategy(raw []byte) (*Doc, error) {
	var sj strategyJSON
	if err := json.Unmarshal(raw, &sj); err != nil {
		return nil, fmt.Errorf("strategy json: %w", err)
	}
	if sj.ID == "" {
		return nil, fmt.Errorf("strategy document missing id")
	}
	doc := &Doc{ID: sj.ID, Kind: "strategy", Revision: sj.Revision, Title: sj.Title, RawBytes: append([]byte(nil), raw...)}
	doc.Payload = map[string]interface{}{
		"schema":   sj.Schema,
		"kind":     sj.Kind,
		"budget":   sj.Budget,
		"frontier": sj.Frontier,
	}
	for _, tg := range sj.Targets {
		toks := map[string]string{}
		for k, v := range tg.Tokens {
			toks[k] = v
		}
		doc.Payload["target:"+tg.ID] = toks
	}
	return doc, nil
}

// Parse dispatches on the declared schema.
func Parse(raw []byte) (*Doc, error) {
	var head struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("source json: %w", err)
	}
	switch {
	case strings.Contains(head.Schema, "strategy"):
		return ParseStrategy(raw)
	default:
		return ParseNet(raw)
	}
}
