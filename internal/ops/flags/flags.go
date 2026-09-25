// Package flags is the CLI argument parser the verbs share: ordered
// parsing of --flag value pairs with validation, a Help short-circuit,
// and the unconsumed Rest for positional arguments.
package flags

import (
	"fmt"
	"strconv"
)

// FlagParser walks the verb's argument list, marking consumed
// flags so the same argument cannot satisfy two flags.
type FlagParser struct {
	args []string
	used map[int]bool
	rest []string
}

// NewFlagParser parses the given arguments; flags may appear anywhere
// before the positional rest.
func NewFlagParser(args []string) *FlagParser {
	return &FlagParser{args: args, used: make(map[int]bool)}
}

// Help prints the verb's help text and reports whether --help or -h was
// present.
func (p *FlagParser) Help(helpText string) bool {
	for _, a := range p.args {
		if a == "--help" || a == "-h" {
			fmt.Println(helpText)
			return true
		}
	}
	return false
}

// String reads one flag's value into dst when the flag is present;
// missing value errors.
func (p *FlagParser) String(flag string, dst *string) error {
	valIdx, found, hasValue := p.take(flag, true)
	if !found {
		return nil
	}
	if !hasValue {
		return fmt.Errorf("%s requires a value", flag)
	}
	*dst = p.args[valIdx]
	return nil
}

// Int reads one integer flag, rejecting values outside the bounds; a
// present flag without a value is ignored.
func (p *FlagParser) Int(flag string, dst *int, min, max int) error {
	valIdx, found, hasValue := p.take(flag, true)
	if !found || !hasValue {
		return nil
	}
	v, err := strconv.Atoi(p.args[valIdx])
	if err != nil {
		return fmt.Errorf("%s: invalid integer", flag)
	}
	if v < min || v > max {
		return fmt.Errorf("%s: must be between %d and %d", flag, min, max)
	}
	*dst = v
	return nil
}

// Bool reads one flag that takes no value.
func (p *FlagParser) Bool(flag string, dst *bool) error {
	if _, found, _ := p.take(flag, false); found {
		*dst = true
	}
	return nil
}

func (p *FlagParser) take(flag string, takesValue bool) (valIdx int, found, hasValue bool) {
	for i, a := range p.args {
		if p.used[i] || a != flag {
			continue
		}
		if !takesValue {
			p.used[i] = true
			return i, true, true
		}
		if i+1 >= len(p.args) {
			return 0, true, false
		}
		p.used[i], p.used[i+1] = true, true
		return i + 1, true, true
	}
	return 0, false, false
}

// Rest returns every unconsumed argument, positional or not.
func (p *FlagParser) Rest() []string {
	if p.rest != nil {
		return p.rest
	}
	p.rest = make([]string, 0)
	for i, a := range p.args {
		if !p.used[i] {
			p.rest = append(p.rest, a)
		}
	}
	return p.rest
}
