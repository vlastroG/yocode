package greet

import "strings"

type Greeter struct{ Name string }

func Hello(name string) string {
	return "Hello, " + clean(name) + "!"
}

func Goodbye(name string) string {
	return "Goodbye, " + clean(name) + "."
}

func (g Greeter) Greet() string {
	return Hello(g.Name)
}

func clean(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "stranger"
	}
	return name
}
