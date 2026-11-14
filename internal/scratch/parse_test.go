package scratch

import (
	"fmt"
	"strings"
	"testing"
)

func show(items []Parsed) string {
	var b strings.Builder
	for _, p := range items {
		var links []string
		for _, l := range p.Links {
			links = append(links, l.Rel+"="+l.Value)
		}
		fmt.Fprintf(&b, "[%s done=%v %q {%s}]", p.Kind, p.Done, p.Text, strings.Join(links, " "))
	}
	return b.String()
}

func TestWhatIsTypedBecomesItems(t *testing.T) {
	cases := []struct{ in, want string }{
		{"just a thought", `[note done=false "just a thought" {}]`},
		{"two lines\nof one note", `[note done=false "two lines\nof one note" {}]`},
		{"::idea a blog post about scratch", `[idea done=false "a blog post about scratch" {}]`},
		{":idea single colon works too", `[idea done=false "single colon works too" {}]`},
		{":follow-up Ask where the go-directives lib ended up", `[follow-up done=false "Ask where the go-directives lib ended up" {}]`},
		{"::fu ping @devtoo about +basalisk #clip CW-20261114-0045", `[follow-up done=false "ping @devtoo about +basalisk #clip CW-20261114-0045" {agent=devtoo project=basalisk tag=clip task=CW-20261114-0045}]`},
		{"$ git rebase -i HEAD~3 #not-a-tag @x", `[cmd done=false "git rebase -i HEAD~3 #not-a-tag @x" {}]`},
		{"[] call the bank", `[todo done=false "call the bank" {}]`},
		{"- [x] sent it", `[todo done=true "sent it" {}]`},
		{"::q does Caster delete?", `[question done=false "does Caster delete?" {}]`},
		{"::bd Post about the sprint", `[blog-draft done=false "Post about the sprint" {}]`},
		// Several in one capture: each directive is its own item, with the lines under it.
		{"loose thought first\n::todo fix the icon\nat all sizes\n::idea reader as playlists",
			`[note done=false "loose thought first" {}][todo done=false "fix the icon\nat all sizes" {}][idea done=false "reader as playlists" {}]`},
		// ::config ties what follows to a project and a task.
		{"::config project=Basalisk, task=CW-20261114-0045\n::todo one\n::idea two",
			`[todo done=false "one" {project=basalisk task=CW-20261114-0045}][idea done=false "two" {project=basalisk task=CW-20261114-0045}]`},
		// Not markers: an address, a heading, a time, a URL with a colon.
		{"mail a@b.co about # of items at 10:30, see https://x.y/z", `[note done=false "mail a@b.co about # of items at 10:30, see https://x.y/z" {}]`},
		{"# A heading\nand text", `[note done=false "# A heading\nand text" {}]`},
		{"[] one\n[] two\n$ ls\nand a note", `[todo done=false "one" {}][todo done=false "two" {}][cmd done=false "ls" {}][note done=false "and a note" {}]`},
		{"::idea one\nstill one\n\nnow a note\n\nwith two paragraphs", `[idea done=false "one\nstill one" {}][note done=false "now a note\n\nwith two paragraphs" {}]`},
		{"", ``},
		{"   \n  ", ``},
		{"::idea", ``},
	}
	for _, c := range cases {
		if got := show(Parse(c.in)); got != c.want {
			t.Errorf("%q\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}
