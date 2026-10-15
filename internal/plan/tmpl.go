package plan

import (
	"specific-harness/internal/prompts"
	"strings"
)

// Render reads the same YAML catalog; explicit legacy directories must contain prompts.yaml.
func Render(dir, name string, data any) (string, error) {
	c, err := prompts.Legacy(dir)
	if err != nil {
		return "", err
	}
	return c.Render("templates."+strings.TrimSuffix(name, ".tmpl"), data)
}
