package vault

import (
	"fmt"
	"slices"

	"github.com/Kaikei-e/DocDag/config"
	"github.com/Kaikei-e/DocDag/model"

	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// SelfCheck reports the two mistakes DocDag's own Validate does not catch, and
// which are exactly the two a generated configuration is most likely to make.
//
//  1. Two rules with one name. Validate checks that a rule has a name and a
//     known severity and nothing else, so a duplicate produces two findings
//     under one name, one fixture directory for both, and no complaint.
//
//  2. An attribute key that names nothing. Attribute keys are frontmatter
//     keys, and any frontmatter key may be undeclared, so a misspelled
//     projection name is read as an absent key: the rule validates, lints
//     clean and fires on nothing for the rest of its life. Every key a rule or
//     a projection reads has to be a declared projection, one of the three the
//     engine answers itself, or a field declared by the kinds the unit is
//     about.
func SelfCheck(cfg config.Config) error {
	if err := checkRuleNames(cfg); err != nil {
		return err
	}
	return checkAttrKeys(cfg)
}

func checkRuleNames(cfg config.Config) error {
	seen := make(map[string]bool, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		if seen[rule.Name] {
			return fmt.Errorf("%w: rule %q is declared twice", ErrConfig, rule.Name)
		}
		seen[rule.Name] = true
	}
	return nil
}

// unit is one thing whose attribute keys are checked: a rule or a projection,
// named for the error message, with the conditions it is evaluated under. A
// projection contributes one root condition per alternative, because the
// alternatives are OR-ed and each may be about a different kind.
type unit struct {
	what  string
	name  string
	roots []config.Condition
}

func units(cfg config.Config) []unit {
	out := make([]unit, 0, len(cfg.Rules)+len(cfg.Projections))
	for _, rule := range cfg.Rules {
		out = append(out, unit{what: "rule", name: rule.Name, roots: []config.Condition{rule.When}})
	}
	for _, spec := range cfg.Projections {
		out = append(out, unit{what: "projection", name: spec.Name, roots: spec.Whens()})
	}
	return out
}

func checkAttrKeys(cfg config.Config) error {
	engine := []string{config.AttrInForce, config.KeyKind, cfg.EffectiveStatus()}
	projections := cfg.ProjectionNames()
	all := cfg.KindNames()
	for _, u := range units(cfg) {
		for _, root := range u.roots {
			if err := checkCondition(cfg, u, root, all, engine, projections); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkCondition walks one condition and everything nested in it, carrying the
// kinds the condition is about. A `kind: {eq: …}` literal narrows the scope for
// the clauses it is ANDed with and for everything below them, which is what
// makes `attr: {kind: {eq: edit}, component: {eq: memory}}` a statement about
// an edit's field rather than about a key nobody declared.
func checkCondition(
	cfg config.Config, u unit, cond config.Condition, scope, engine, projections []string,
) error {
	if match, ok := cond.Attr[config.KeyKind]; ok && match.Eq != nil {
		scope = []string{*match.Eq}
	}
	known := knownKeys(cfg, engine, projections, scope)
	for key := range cond.Attr {
		if !slices.Contains(known, key) {
			return fmt.Errorf(
				"%w: %s %q reads the attribute %q, which is neither a projection nor a field of %v",
				ErrConfig, u.what, u.name, key, scope)
		}
	}
	// A one-hop clause reads the neighbour's attributes, so it is checked
	// against the kinds the edge's far end may have rather than against the
	// kinds the condition itself is about.
	for _, clause := range cond.ViaClauses() {
		neighbours := neighbourKinds(cfg, clause)
		reachable := knownKeys(cfg, engine, projections, neighbours)
		for key := range clause.Attr {
			if !slices.Contains(reachable, key) {
				return fmt.Errorf(
					"%w: %s %q reads the attribute %q across %s %q, which is neither a projection nor a field of %v",
					ErrConfig, u.what, u.name, key, clause.Key(), clause.Edge, neighbours)
			}
		}
	}
	for _, alternative := range cond.AnyOf {
		if err := checkCondition(cfg, u, alternative, scope, engine, projections); err != nil {
			return err
		}
	}
	if cond.Not != nil {
		return checkCondition(cfg, u, *cond.Not, scope, engine, projections)
	}
	return nil
}

// neighbourKinds returns the kinds at the far end of a one-hop clause: the
// edge's `to` for a forward hop and its `from` for an inbound one. An edge
// that constrains neither endpoint may reach any kind.
func neighbourKinds(cfg config.Config, clause config.ViaClause) []string {
	spec, ok := cfg.Edge(model.EdgeType(clause.Edge))
	if !ok {
		return cfg.KindNames()
	}
	kinds := spec.To
	if clause.Inbound {
		kinds = spec.From
	}
	if len(kinds) == 0 {
		return cfg.KindNames()
	}
	return slices.Sorted(slices.Values(kinds))
}

// knownKeys returns every attribute key a unit scoped to these kinds may read.
func knownKeys(cfg config.Config, engine, projections, kinds []string) []string {
	known := append(slices.Clone(engine), projections...)
	for _, kind := range kinds {
		for name := range cfg.FieldSpecs(kind) {
			known = append(known, name)
		}
		if period, ok := cfg.KindPeriod(kind); ok {
			known = append(known, period.Fields()...)
		}
	}
	slices.Sort(known)
	return slices.Compact(known)
}

// RuleNames returns the names of the rules uzushio adds, which is what a
// fixture writer and a lint report both index by.
func RuleNames() []string { return vocab.Strings(vocab.AllRules()) }
