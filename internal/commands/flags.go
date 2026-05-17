package commands

import (
	"fmt"
	"strconv"
)

type FlagParser struct {
	args []string
	pos  int
	rest []string
}

func NewFlagParser(args []string) *FlagParser {
	return &FlagParser{args: args}
}

func (p *FlagParser) Help(helpText string) bool {
	for _, a := range p.args {
		if a == "--help" || a == "-h" {
			fmt.Println(helpText)
			return true
		}
	}
	return false
}

func (p *FlagParser) String(flag string, dst *string) error {
	for i := p.pos; i < len(p.args); i++ {
		if p.args[i] == flag {
			if i+1 >= len(p.args) {
				return fmt.Errorf("%s requires a value", flag)
			}
			*dst = p.args[i+1]
			p.pos = i + 2
			return nil
		}
	}
	return nil
}

func (p *FlagParser) Int(flag string, dst *int, min, max int) error {
	for i := p.pos; i < len(p.args); i++ {
		if p.args[i] == flag && i+1 < len(p.args) {
			v, err := strconv.Atoi(p.args[i+1])
			if err != nil {
				return fmt.Errorf("%s: invalid integer", flag)
			}
			if v < min || v > max {
				return fmt.Errorf("%s: must be between %d and %d", flag, min, max)
			}
			*dst = v
			p.pos = i + 2
			return nil
		}
	}
	return nil
}

func (p *FlagParser) Bool(flag string, dst *bool) error {
	for i := p.pos; i < len(p.args); i++ {
		if p.args[i] == flag {
			*dst = true
			p.pos = i + 1
			return nil
		}
	}
	return nil
}

func (p *FlagParser) Rest() []string {
	if p.rest != nil {
		return p.rest
	}
	p.rest = make([]string, 0)
	for i := p.pos; i < len(p.args); i++ {
		p.rest = append(p.rest, p.args[i])
	}
	return p.rest
}
