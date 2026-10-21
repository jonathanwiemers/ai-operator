package ui

import (
	"fmt"
	"sort"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
)

// ClaimMapper turns the claims of a login into a user name and roles.
type ClaimMapper struct {
	username cel.Program
	roles    cel.Program
}

// NewClaimMapper compiles the claim expressions.
func NewClaimMapper(c Claims) (*ClaimMapper, error) {
	env, err := cel.NewEnv(cel.Variable("claims", cel.MapType(cel.StringType, cel.DynType)))
	if err != nil {
		return nil, err
	}
	compile := func(field, expr string) (cel.Program, error) {
		ast, iss := env.Compile(expr)
		if iss.Err() != nil {
			return nil, fmt.Errorf("oidc.claims.%s: %w", field, iss.Err())
		}
		prg, err := env.Program(ast)
		if err != nil {
			return nil, fmt.Errorf("oidc.claims.%s: %w", field, err)
		}
		return prg, nil
	}
	m := &ClaimMapper{}
	if m.username, err = compile("username", c.Username); err != nil {
		return nil, err
	}
	if m.roles, err = compile("roles", c.Roles); err != nil {
		return nil, err
	}
	return m, nil
}

// Map evaluates the expressions. Roles come back sorted and without
// duplicates.
func (m *ClaimMapper) Map(claims map[string]any) (username string, roles []string, err error) {
	vars := map[string]any{"claims": claims}
	out, _, err := m.username.Eval(vars)
	if err != nil {
		return "", nil, fmt.Errorf("username: %w", err)
	}
	username, ok := out.Value().(string)
	if !ok || username == "" {
		return "", nil, fmt.Errorf("username: want a non-empty string, got %v", out.Value())
	}
	// A user without roles often has no roles claim at all (Zitadel), so a
	// claim the expression cannot find means no roles.
	out, _, err = m.roles.Eval(vars)
	if err != nil {
		return username, []string{}, nil
	}
	roles, err = stringsOf(out)
	if err != nil {
		return "", nil, fmt.Errorf("roles: %w", err)
	}
	return username, roles, nil
}

// stringsOf accepts a string, a list of strings or a map with string keys.
func stringsOf(v ref.Val) ([]string, error) {
	set := map[string]bool{}
	add := func(e ref.Val) error {
		s, ok := e.Value().(string)
		if !ok {
			return fmt.Errorf("want strings, got %v", e.Value())
		}
		if s != "" {
			set[s] = true
		}
		return nil
	}
	switch val := v.(type) {
	case traits.Iterable: // lists, and maps by their keys
		for it := val.Iterator(); it.HasNext() == types.True; {
			if err := add(it.Next()); err != nil {
				return nil, err
			}
		}
	default:
		if err := add(v); err != nil {
			return nil, fmt.Errorf("want a list of strings, a string or a map, got %v", v.Value())
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}
