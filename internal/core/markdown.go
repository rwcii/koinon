package core

import (
	"html/template"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// renderMarkdown renders the fixed Markdown subset of decision card text (#206, chunk 03):
// paragraphs, line breaks, ordered and unordered lists, emphasis, inline code and fenced
// code blocks. Every byte of the input is escaped; the renderer writes only the tags of
// that subset. Raw HTML, links and images stay literal text.
func renderMarkdown(text string) template.HTML {
	var b strings.Builder
	lines := strings.Split(text, "\n")
	var para []string
	var list *markdownList
	flushPara := func() {
		if len(para) > 0 {
			b.WriteString("<p>")
			writeLines(&b, para)
			b.WriteString("</p>")
			para = nil
		}
	}
	flushList := func() {
		if list != nil {
			list.write(&b)
			list = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimLeft(line, " ")
		if fence := fenceOf(trimmed); fence != "" && len(line)-len(trimmed) < 4 {
			flushPara()
			flushList()
			var code []string
			for i++; i < len(lines); i++ {
				if close := strings.TrimSpace(lines[i]); strings.HasPrefix(close, fence) && strings.Trim(close, fence[:1]) == "" {
					break
				}
				code = append(code, lines[i])
			}
			b.WriteString("<pre><code>")
			template.HTMLEscape(&b, []byte(strings.Join(code, "\n")))
			b.WriteString("</code></pre>")
			continue
		}
		if strings.TrimSpace(line) == "" {
			flushPara()
			flushList()
			continue
		}
		if ordered, start, rest, ok := listItem(trimmed); ok && len(line)-len(trimmed) < 4 {
			flushPara()
			if list == nil || list.ordered != ordered {
				flushList()
				list = &markdownList{ordered: ordered, start: start}
			}
			list.items = append(list.items, []string{rest})
			continue
		}
		if list != nil {
			// A following line continues the last item.
			last := &list.items[len(list.items)-1]
			*last = append(*last, strings.TrimSpace(line))
			continue
		}
		para = append(para, line)
	}
	flushPara()
	flushList()
	return template.HTML(b.String())
}

type markdownList struct {
	ordered bool
	start   int
	items   [][]string
}

func (l *markdownList) write(b *strings.Builder) {
	if l.ordered {
		if l.start != 1 {
			b.WriteString(`<ol start="` + strconv.Itoa(l.start) + `">`)
		} else {
			b.WriteString("<ol>")
		}
	} else {
		b.WriteString("<ul>")
	}
	for _, item := range l.items {
		b.WriteString("<li>")
		writeLines(b, item)
		b.WriteString("</li>")
	}
	if l.ordered {
		b.WriteString("</ol>")
	} else {
		b.WriteString("</ul>")
	}
}

// writeLines writes the lines of one block with their line breaks kept.
func writeLines(b *strings.Builder, lines []string) {
	for i, line := range lines {
		if i > 0 {
			b.WriteString("<br>")
		}
		writeInline(b, line)
	}
}

// fenceOf returns the opening fence of a fenced code block line: three or more backticks or
// tildes. The info string after it is ignored.
func fenceOf(line string) string {
	for _, c := range "`~" {
		n := 0
		for n < len(line) && rune(line[n]) == c {
			n++
		}
		if n >= 3 && (c != '`' || !strings.Contains(line[n:], "`")) {
			return line[:n]
		}
	}
	return ""
}

// listItem recognizes "- ", "* ", "+ " and "N. " or "N) " list markers.
func listItem(line string) (ordered bool, start int, rest string, ok bool) {
	if len(line) >= 2 && strings.ContainsRune("-*+", rune(line[0])) && line[1] == ' ' {
		return false, 0, strings.TrimSpace(line[2:]), true
	}
	n := 0
	for n < len(line) && n < 9 && line[n] >= '0' && line[n] <= '9' {
		n++
	}
	if n > 0 && n+1 < len(line) && (line[n] == '.' || line[n] == ')') && line[n+1] == ' ' {
		start, _ = strconv.Atoi(line[:n])
		return true, start, strings.TrimSpace(line[n+2:]), true
	}
	return false, 0, "", false
}

// writeInline writes one line with inline code and emphasis. A delimiter without its match
// is literal text. Each search for a closing delimiter is remembered when it fails, so a
// line of unmatched delimiters takes linear time.
func writeInline(b *strings.Builder, s string) {
	missing := map[string]bool{}
	var prev rune
	for s != "" {
		i := strings.IndexAny(s, "`*_")
		if i < 0 {
			template.HTMLEscape(b, []byte(s))
			return
		}
		if i > 0 {
			template.HTMLEscape(b, []byte(s[:i]))
			prev, _ = utf8.DecodeLastRuneInString(s[:i])
			s = s[i:]
		}
		if s[0] == '`' {
			n := 1
			for n < len(s) && s[n] == '`' {
				n++
			}
			if end := closingTicks(s[n:], n, missing); end >= 0 {
				code := s[n : n+end]
				if len(code) > 1 && code[0] == ' ' && code[len(code)-1] == ' ' && strings.Trim(code, " ") != "" {
					code = code[1 : len(code)-1]
				}
				b.WriteString("<code>")
				template.HTMLEscape(b, []byte(code))
				b.WriteString("</code>")
				s = s[n+end+n:]
				prev = '`'
				continue
			}
			b.WriteString(s[:n])
			s, prev = s[n:], '`'
			continue
		}
		if n, end := emphasis(s, prev, missing); n > 0 {
			tag := "em"
			if n == 2 {
				tag = "strong"
			}
			b.WriteString("<" + tag + ">")
			writeInline(b, s[n:n+end])
			b.WriteString("</" + tag + ">")
			s, prev = s[n+end+n:], rune(s[0])
			continue
		}
		b.WriteByte(s[0])
		s, prev = s[1:], rune(s[0])
	}
}

// closingTicks finds a run of exactly n backticks in s and returns its offset, or -1.
func closingTicks(s string, n int, missing map[string]bool) int {
	ticks := strings.Repeat("`", n)
	if missing[ticks] {
		return -1
	}
	for from := 0; ; {
		j := strings.Index(s[from:], ticks)
		if j < 0 {
			missing[ticks] = true
			return -1
		}
		j += from
		k := j + n
		for k < len(s) && s[k] == '`' {
			k++
		}
		if k-j == n {
			return j
		}
		from = k
	}
}

// emphasis matches "**text**", "__text__", "*text*" or "_text_" at the start of s and
// returns the delimiter length and the offset of the closing delimiter from the end of the
// opening one. The text must not start or end with a space. An underscore does not open or
// close inside a word, so names such as decision_get stay literal.
func emphasis(s string, prev rune, missing map[string]bool) (int, int) {
	c := s[0]
	for _, n := range []int{2, 1} {
		if len(s) < 2*n+1 || strings.Count(s[:n], string(c)) != n {
			continue
		}
		delim := s[:n]
		if missing[delim] || s[n] == ' ' || s[n] == c || (c == '_' && wordRune(prev)) {
			continue
		}
		end := strings.Index(s[n:], delim)
		if end < 0 {
			missing[delim] = true
			continue
		}
		if end == 0 || s[n+end-1] == ' ' {
			continue
		}
		if c == '_' {
			if next, _ := utf8.DecodeRuneInString(s[n+end+n:]); wordRune(next) {
				continue
			}
		}
		return n, end
	}
	return 0, 0
}

func wordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
