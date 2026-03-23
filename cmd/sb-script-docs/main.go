package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/slidebolt/sb-script/internal/engine"
)

func main() {
	ref := engine.APIReferenceDoc()

	if err := os.MkdirAll("docs", 0o755); err != nil {
		panic(err)
	}

	jsonPath := filepath.Join("docs", "scripting-api.json")
	mdPath := filepath.Join("docs", "scripting-api.md")

	data, err := json.MarshalIndent(ref, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(jsonPath, append(data, '\n'), 0o644); err != nil {
		panic(err)
	}

	md := renderMarkdown(ref)
	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		panic(err)
	}
}

func renderMarkdown(ref engine.APIReference) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Scripting API\n\n")
	fmt.Fprintf(&b, "Language: `%s`\n\n", ref.Language)
	fmt.Fprintf(&b, "Version: `%d`\n\n", ref.Version)

	renderSection := func(title string, docs []engine.APIDoc) {
		fmt.Fprintf(&b, "## %s\n\n", title)
		for _, doc := range docs {
			fmt.Fprintf(&b, "### `%s`\n\n", doc.Signature)
			fmt.Fprintf(&b, "%s\n\n", doc.Description)
			if len(doc.Params) > 0 {
				fmt.Fprintf(&b, "**Parameters**\n\n")
				for _, p := range doc.Params {
					fmt.Fprintf(&b, "- `%s` `%s`: %s\n", p.Name, p.Type, p.Description)
					for _, f := range p.Fields {
						fmt.Fprintf(&b, "  - `%s` `%s`: %s\n", f.Name, f.Type, f.Description)
					}
				}
				fmt.Fprintln(&b)
			}
			if doc.Returns != "" {
				fmt.Fprintf(&b, "**Returns**: `%s`\n\n", doc.Returns)
			}
			if len(doc.Examples) > 0 {
				fmt.Fprintf(&b, "**Examples**\n\n")
				for _, ex := range doc.Examples {
					fmt.Fprintf(&b, "- `%s`\n", ex)
				}
				fmt.Fprintln(&b)
			}
		}
	}

	renderSection("Globals", ref.Globals)
	renderSection("Context Methods", ref.ContextMethods)

	fmt.Fprintf(&b, "## Example Scripts\n\n")
	examples, _ := filepath.Glob(filepath.Join("cmd", "sb-script", "features", "*.lua"))
	sort.Strings(examples)
	for _, ex := range examples {
		body, err := os.ReadFile(ex)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "### `%s`\n\n```lua\n%s\n```\n\n", filepath.Base(ex), strings.TrimSpace(string(body)))
	}

	return b.String()
}
