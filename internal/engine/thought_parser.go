package engine

import "strings"

// ThoughtParser separates <thought> tags from content in streaming chunks.
type ThoughtParser struct {
	InThought bool
	Buffer    string
	OnThought func(string)
	OnText    func(string)
}

func (p *ThoughtParser) Parse(chunk string) {
	if chunk == "" {
		p.flush()
		return
	}

	p.Buffer += chunk
	for {
		target := "<thought>"
		if p.InThought {
			target = "</thought>"
		}

		idx := strings.Index(p.Buffer, target)
		if idx == -1 {
			possibleTagStart := strings.LastIndexAny(p.Buffer, "<")
			if possibleTagStart != -1 {
				remaining := p.Buffer[possibleTagStart:]
				if strings.HasPrefix(target, remaining) {
					p.emit(p.Buffer[:possibleTagStart])
					p.Buffer = remaining
					return
				}
			}
			p.emit(p.Buffer)
			p.Buffer = ""
			return
		}

		p.emit(p.Buffer[:idx])
		p.Buffer = p.Buffer[idx+len(target):]
		p.InThought = !p.InThought
	}
}

func (p *ThoughtParser) emit(text string) {
	if text == "" {
		return
	}
	if p.InThought {
		p.OnThought(text)
	} else {
		p.OnText(text)
	}
}

func (p *ThoughtParser) flush() {
	if p.Buffer == "" {
		return
	}
	p.emit(p.Buffer)
	p.Buffer = ""
}
