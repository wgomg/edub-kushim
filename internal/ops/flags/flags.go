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
	pos  int
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
	for i := 0; i < len(p.args); i++ {
		if p.used[i] {
			continue
		}
		if p.args[i] == flag {
			if i+1 >= len(p.args) {
				return fmt.Errorf("%s requires a value", flag)
			}
			*dst = p.args[i+1]
			p.used[i] = true
			p.used[i+1] = true
			if i >= p.pos {
				p.pos = i + 2
			}
			return nil
		}
	}
	return nil
}

// Int reads one integer flag, clamped to the bounds.
func (p *FlagParser) Int(flag string, dst *int, min, max int) error {
	for i := 0; i < len(p.args); i++ {
		if p.used[i] {
			continue
		}
		if p.args[i] == flag && i+1 < len(p.args) {
			v, err := strconv.Atoi(p.args[i+1])
			if err != nil {
				return fmt.Errorf("%s: invalid integer", flag)
			}
			if v < min || v > max {
				return fmt.Errorf("%s: must be between %d and %d", flag, min, max)
			}
			*dst = v
			p.used[i] = true
			p.used[i+1] = true
			if i >= p.pos {
				p.pos = i + 2
			}
			return nil
		}
	}
	return nil
}

// Bool reads one flag that takes no value.
func (p *FlagParser) Bool(flag string, dst *bool) error {
	for i := 0; i < len(p.args); i++ {
		if p.used[i] {
			continue
		}
		if p.args[i] == flag {
			*dst = true
			p.used[i] = true
			if i >= p.pos {
				p.pos = i + 1
			}
			return nil
		}
	}
	return nil
}

// Rest returns the positional arguments after the last parsed flag.
func (p *FlagParser) Rest() []string {
	if p.rest != nil {
		return p.rest
	}
	p.rest = make([]string, 0)
	for i := 0; i < len(p.args); i++ {
		if !p.used[i] {
			p.rest = append(p.rest, p.args[i])
		}
	}
	return p.rest
}
