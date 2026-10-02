package common

import (
	"fmt"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/somaz94/agentport/internal/frontmatter"
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
	err = Fields(item, doc, func(item *ir.Item, key string, n *yaml.Node) (bool, error) {
		if key != "name" {
			return claim(item, key, n)
		}
		if text := Text(n); text != "" {
			item.Name = text
		}
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	return item, nil
}

// Fields fills item from doc's frontmatter: the description directly, the keys claim consumes
// through it, and every other key as an extension.
func Fields(item *ir.Item, doc *frontmatter.Document, claim Claim) error {
	for _, key := range doc.Keys() {
		n, _ := doc.Get(key)
		if key == "description" {
			item.Description = Text(n)
			continue
		}
		handled, err := claim(item, key, n)
		if err != nil {
			return err
		}
		if !handled {
			item.Extensions = append(item.Extensions, ir.Field{Key: key, Value: n})
		}
	}
	return nil
}
