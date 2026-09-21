package controller

import (
	"fmt"
	"sort"
)

// constructors maps registry names to controller factories.
var constructors = map[string]func(Config) Controller{
	"naive":  func(Config) Controller { return Naive{} },
	"greedy": func(c Config) Controller { return NewGreedy(c) },
	"lp":     func(c Config) Controller { return NewLP(c) },
}

// New builds a controller by registry name.
func New(name string, cfg Config) (Controller, error) {
	ctor, ok := constructors[name]
	if !ok {
		return nil, fmt.Errorf("unknown controller %q (known: %v)", name, Names())
	}
	return ctor(cfg), nil
}

// Names lists the registered controllers in sorted order.
func Names() []string {
	names := make([]string, 0, len(constructors))
	for n := range constructors {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
