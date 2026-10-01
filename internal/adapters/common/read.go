package common

import (
	"fmt"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/skilldir"
)

// Claim handles one frontmatter key a harness models and reports whether it consumed the key.
// Keys it does not consume become extensions.
type Claim func(item *ir.Item, key string, n *yaml.Node) (bool, error)

// ReadSkill reads the skill directory dir as harness h. The name defaults to the directory name,
// as every harness does.
func ReadSkill(dir string, h harness.ID, claim Claim) (*ir.Item, error) {
	doc, res, notes, err := skilldir.Read(dir)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	item := &ir.Item{
		Kind:       ir.KindSkill,
		Name:       filepath.Base(abs),
		Body:       doc.Body,
		Invocation: ir.Invocation{UserInvocable: true, ModelInvocable: true},
		Resources:  res,
		Notes:      notes,
		Source:     ir.Source{Harness: h, Path: dir},
	}
	for _, key := range doc.Keys() {
		n, _ := doc.Get(key)
		switch key {
		case "name":
			if text := Text(n); text != "" {
				item.Name = text
			}
		case "description":
			item.Description = Text(n)
		default:
			handled, err := claim(item, key, n)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", dir, err)
			}
			if !handled {
				item.Extensions = append(item.Extensions, ir.Field{Key: key, Value: n})
			}
		}
	}
	return item, nil
}
