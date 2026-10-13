package docs

import "strings"

// splitFrontMatter removes a Hugo/Jekyll front matter block (YAML between "---" or TOML between
// "+++") and returns the page title ("title — description") plus the remaining body. Without
// front matter the title is the first "# " heading. The title gives every chunk of a page its
// context, which body chunks under generic headings would otherwise lack.
func splitFrontMatter(content string) (title, body string) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	body = content
	var desc string
	for delim, sep := range map[string]string{"---": ":", "+++": "="} {
		if !strings.HasPrefix(content, delim+"\n") {
			continue
		}
		start := len(delim) + 1
		end := strings.Index(content[start:], "\n"+delim)
		if end < 0 {
			break
		}
		header := content[start : start+end]
		body = content[start+end+1+len(delim):]
		for _, line := range strings.Split(header, "\n") {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				continue // nested key
			}
			key, val, ok := strings.Cut(line, sep)
			if !ok {
				continue
			}
			val = strings.Trim(strings.TrimSpace(val), `"'`)
			switch strings.TrimSpace(key) {
			case "title":
				title = val
			case "description":
				desc = val
			}
		}
		break
	}
	if title == "" {
		title = firstH1(body)
	}
	if desc != "" {
		if title == "" {
			return desc, body
		}
		title += " — " + desc
	}
	return title, body
}

func firstH1(body string) string {
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
		} else if !inFence && strings.HasPrefix(t, "# ") {
			return strings.TrimSpace(t[2:])
		}
	}
	return ""
}
