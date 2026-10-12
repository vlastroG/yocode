package docs

import "strings"

type Chunk struct {
	Source  string // e.g. "project/docs/install.md"
	Heading string // nearest markdown heading above the chunk
	Text    string
}

// Split cuts content into chunks of at most maxChars bytes (for ASCII text), starting a new
// chunk at every markdown heading outside code fences.
func Split(source, content string, maxChars int) []Chunk {
	var (
		chunks  []Chunk
		buf     strings.Builder
		heading string
		inFence bool
	)
	flush := func() {
		if text := strings.TrimSpace(buf.String()); text != "" {
			chunks = append(chunks, Chunk{Source: source, Heading: heading, Text: text})
		}
		buf.Reset()
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
		} else if h, ok := headingText(trimmed); ok && !inFence {
			flush()
			heading = h
		}
		for _, piece := range splitLong(line, maxChars) {
			if buf.Len() > 0 && buf.Len()+len(piece) > maxChars {
				flush()
			}
			buf.WriteString(piece)
			buf.WriteByte('\n')
		}
	}
	flush()
	return chunks
}

func headingText(line string) (string, bool) {
	rest := strings.TrimLeft(line, "#")
	if len(rest) == len(line) || !strings.HasPrefix(rest, " ") {
		return "", false
	}
	t := strings.TrimSpace(rest)
	return t, t != ""
}

func splitLong(line string, max int) []string {
	if len(line) <= max {
		return []string{line}
	}
	var parts []string
	r := []rune(line)
	for len(r) > 0 {
		n := min(max, len(r))
		parts = append(parts, string(r[:n]))
		r = r[n:]
	}
	return parts
}
