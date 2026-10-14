package core

import (
	"strings"
	"testing"
	"time"
)

func TestRenderMarkdownSubset(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"paragraphs and line breaks", "one\ntwo\n\nthree", "<p>one<br>two</p><p>three</p>"},
		{"unordered list", "- a\n* b\n+ c", "<ul><li>a</li><li>b</li><li>c</li></ul>"},
		{"ordered list with start", "3. a\n4) b", `<ol start="3"><li>a</li><li>b</li></ol>`},
		{"list item continuation", "1. a\n   more\n2. b", "<ol><li>a<br>more</li><li>b</li></ol>"},
		{"list then paragraph", "- a\n\npara", "<ul><li>a</li></ul><p>para</p>"},
		{"emphasis", "*em* and **strong** and _em_ and __strong__", "<p><em>em</em> and <strong>strong</strong> and <em>em</em> and <strong>strong</strong></p>"},
		{"intraword underscores stay literal", "call decision_get and snake_case_name", "<p>call decision_get and snake_case_name</p>"},
		{"unmatched delimiters stay literal", "a * b ** c _ d `e", "<p>a * b ** c _ d `e</p>"},
		{"inline code", "run `koinon status` now", "<p>run <code>koinon status</code> now</p>"},
		{"inline code keeps emphasis literal", "`*x*`", "<p><code>*x*</code></p>"},
		{"double backtick code", "``a ` b``", "<p><code>a ` b</code></p>"},
		{"fenced code", "```sh\necho *x* <b>\n```\nafter", "<pre><code>echo *x* &lt;b&gt;</code></pre><p>after</p>"},
		{"tilde fence and unclosed fence", "~~~\na\n\nb", "<pre><code>a\n\nb</code></pre>"},
		{"non-ASCII code for reading", "```\nprint(\"héllo ✓\")\n```", "<pre><code>print(&#34;héllo ✓&#34;)</code></pre>"},
		{"raw HTML is inert", "<script>alert(1)</script><img src=x onerror=alert(1)>", "<p>&lt;script&gt;alert(1)&lt;/script&gt;&lt;img src=x onerror=alert(1)&gt;</p>"},
		{"links and images are literal", "[x](javascript:alert(1)) ![i](http://e/i.png)", "<p>[x](javascript:alert(1)) ![i](http://e/i.png)</p>"},
		{"HTML in emphasis and code is escaped", "**<i>x</i>** `<b>`", "<p><strong>&lt;i&gt;x&lt;/i&gt;</strong> <code>&lt;b&gt;</code></p>"},
		{"quotes and ampersands", `a "b" & 'c'`, "<p>a &#34;b&#34; &amp; &#39;c&#39;</p>"},
		{"tab is kept", "a\tb", "<p>a\tb</p>"},
	} {
		if got := string(renderMarkdown(tc.in)); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// TestRenderMarkdownOnlyEmitsSubsetTags renders hostile input and checks that every tag in
// the output is one the subset writes.
func TestRenderMarkdownOnlyEmitsSubsetTags(t *testing.T) {
	in := "<a href=\"javascript:x\">a</a>\n<iframe src=//e></iframe>\n- <svg onload=x>\n```\n</code></pre><script>x</script>\n```\n*<style>*"
	allowed := map[string]bool{"p": true, "br": true, "ul": true, "ol": true, "li": true, "em": true, "strong": true, "code": true, "pre": true}
	out := string(renderMarkdown(in))
	for i := strings.IndexByte(out, '<'); i >= 0; i = strings.IndexByte(out, '<') {
		out = out[i+1:]
		name := strings.TrimPrefix(out[:strings.IndexAny(out, " >")], "/")
		if !allowed[name] {
			t.Fatalf("tag %q in output", name)
		}
	}
}

// TestRenderMarkdownLinearTime renders the largest context field filled with unmatched
// delimiters; each failed search is remembered, so this stays fast.
func TestRenderMarkdownLinearTime(t *testing.T) {
	for _, unit := range []string{"*a ", "**a ", "_a ", "`a ", "``a ", "a_", "* "} {
		in := strings.Repeat(unit, decisionContextLimit/len(unit))
		start := time.Now()
		renderMarkdown(in)
		if d := time.Since(start); d > time.Second {
			t.Fatalf("%q repeated took %v", unit, d)
		}
	}
}
