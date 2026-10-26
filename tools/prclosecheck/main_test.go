package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the shipped CLI with real Git graphs and event JSON, rather than
// calling its parser directly or replacing Git with a successful stub.
func TestCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "prclosecheck")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, ".") //nolint:gosec // fixed tool and test-owned output
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	fixture := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, tc := range []struct {
		name, message, body, want                   string
		baseSync, missingBody, missingBase, shallow bool
	}{
		{name: "historical_false_closure", message: fixture("a6752329.txt"), body: fixture("pr1639.md"), want: "ivanarama/onebase#1274"},
		{name: "declared", message: "fix: solution\n\nFixes #42", body: "Fixes #42"},
		{name: "ordinary_mention", message: "fix: relates to #42", body: ""},
		{name: "negative_raw_example", message: "there is no `Fixes #42`", body: "`Fixes #42`", want: "#42"},
		{name: "body_must_be_standalone", message: "Fixes #42", body: "This does not say Fixes #42", want: "#42"},
		{name: "fenced", message: "Fixes #42", body: "```text\nFixes #42\n```", want: "#42"},
		{name: "fence_length", message: "Fixes #42", body: "````text\n```\nFixes #42\n````", want: "#42"},
		{name: "indented_fence_does_not_close", message: "Fixes #42", body: "```text\n    ```\nFixes #42\n```", want: "#42"},
		{name: "fence_info_is_not_close", message: "Fixes #42", body: "~~~text\n~~~not-a-close\nFixes #42\n~~~", want: "#42"},
		{name: "list_fence", message: "Fixes #42", body: "- ```text\n  Fixes #42\n  ```", want: "#42"},
		{name: "quote_fence_container_ends", message: "Fixes #42", body: "> ```\n> example\n\n```\nFixes #42\n```", want: "#42"},
		{name: "list_fence_container_ends", message: "Fixes #42", body: "- ```\n  example\n\n```\nFixes #42\n```", want: "#42"},
		{name: "ordered_list_fence_container_ends", message: "Fixes #42", body: "1. ~~~\n   example\n\n~~~\nFixes #42\n~~~", want: "#42"},
		{name: "nested_quote_fence_container_ends", message: "Fixes #42", body: "> > ~~~\n> > example\n\n~~~\nFixes #42\n~~~", want: "#42"},
		{name: "continued_list_fence_container_ends", message: "Fixes #42", body: "- item\n\n  ```\n  example\n\n```\nFixes #42\n```", want: "#42"},
		{name: "tab_list_fence_container_ends", message: "Fixes #42", body: "- item\n\n\t```\n\texample\n\n```\nFixes #42\n```", want: "#42"},
		{name: "indented_fence_conservative", message: "Fixes #42", body: " ```\nexample\n```\n\nFixes #42", want: "#42"},
		{name: "quote_fence_conservative", message: "Fixes #42", body: "> ```\n> example\n> ```\n\nFixes #42", want: "#42"},
		{name: "list_fence_conservative", message: "Fixes #42", body: "- ```\n  example\n  ```\n\nFixes #42", want: "#42"},
		{name: "declaration_before_container_fence", message: "Fixes #42", body: "Fixes #42\n\n> ```\n> example"},
		{name: "nested_fence_cannot_declare_later_reference", message: "Fixes #42\nFixes #43", body: "Fixes #42\n\n- ```\n  example\n\n```\nFixes #43\n```", want: "#43"},
		{name: "indented_code", message: "Fixes #42", body: "    Fixes #42", want: "#42"},
		{name: "tab_code", message: "Fixes #42", body: "\tFixes #42", want: "#42"},
		{name: "mixed_tab_code", message: "Fixes #42", body: " \tFixes #42", want: "#42"},
		{name: "comment_in_fence_info", message: "Fixes #42", body: "``` <!-- demo -->\nFixes #42\n```", want: "#42"},
		{name: "comment_in_multiline_span", message: "Fixes #42", body: "`<!-- demo -->\nFixes #42\n`", want: "#42"},
		{name: "multiline_span", message: "Fixes #42", body: "`example\nFixes #42\n`", want: "#42"},
		{name: "html_comment", message: "Fixes #42", body: "<!--\nFixes #42\n-->", want: "#42"},
		{name: "inline_comment", message: "Fixes #42", body: "<!-- note -->Fixes #42", want: "#42"},
		{name: "multiple_comments", message: "Fixes #42", body: "<!-- x --><!--\nFixes #42\n-->", want: "#42"},
		{name: "html_cdata_with_blank", message: "Fixes #42", body: "<![CDATA[\n\nFixes #42\n\n]]>", want: "#42"},
		{name: "html_cdata_in_block", message: "Fixes #42", body: "<div>\n<![CDATA[\n\nFixes #42\n\n]]>\n</div>", want: "#42"},
		{name: "html_cdata_ignores_fences_comments", message: "Fixes #42", body: "<![CDATA[\n<!--\n```\n\nFixes #42\n\n]]>", want: "#42"},
		{name: "html_cdata_does_not_end_at_gt", message: "Fixes #42", body: "<![CDATA[>\n\nFixes #42\n\n]]>", want: "#42"},
		{name: "html_unclosed_cdata", message: "Fixes #42", body: "<![CDATA[\n\nFixes #42", want: "#42"},
		{name: "declaration_after_html_cdata", message: "Fixes #42", body: "<![CDATA[\n<!-- ```\nexample\n]]>\n\nFixes #42"},
		{name: "html_processing_instruction", message: "Fixes #42", body: "<?target\n\nFixes #42\n\n?>", want: "#42"},
		{name: "html_processing_instruction_does_not_end_at_gt", message: "Fixes #42", body: "<?target>\n\nFixes #42\n\n?>", want: "#42"},
		{name: "declaration_after_html_processing_instruction", message: "Fixes #42", body: "<?target\n<!-- ```\n?>\n\nFixes #42"},
		{name: "html_declaration", message: "Fixes #42", body: "<!DOCTYPE\n\nFixes #42\n\n>", want: "#42"},
		{name: "declaration_after_html_declaration", message: "Fixes #42", body: "<!DOCTYPE\nexample>\n\nFixes #42"},
		{name: "html_code", message: "Fixes #42", body: "<pre>\nFixes #42\n</pre>", want: "#42"},
		{name: "html_comment_in_block", message: "Fixes #42", body: "<div>\n<!--\n\nFixes #42\n\n-->\n</div>", want: "#42"},
		{name: "html_pre_in_block", message: "Fixes #42", body: "<div>\n<pre>\n\nFixes #42\n\n</pre>\n</div>", want: "#42"},
		{name: "html_code_with_blank", message: "Fixes #42", body: "<code>\n\nFixes #42\n\n</code>", want: "#42"},
		{name: "html_comment_same_line", message: "Fixes #42", body: "<div><!--\n\nFixes #42\n\n--></div>", want: "#42"},
		{name: "html_code_after_comment", message: "Fixes #42", body: "<div>\n<!-- comment --><CoDe class=\"example\">\n\nFixes #42\n\n</CoDe>\n</div>", want: "#42"},
		{name: "html_comment_hides_code_close", message: "Fixes #42", body: "<div>\n<!-- <pre> -->\n<!--\n</pre>\n\nFixes #42\n\n-->\n</div>", want: "#42"},
		{name: "html_raw_pre_with_blank", message: "Fixes #42", body: "<pre>\n\nFixes #42\n\n</pre>", want: "#42"},
		{name: "html_unclosed_comment_in_block", message: "Fixes #42", body: "<div>\n<!--\n\nFixes #42", want: "#42"},
		{name: "html_unclosed_code_in_block", message: "Fixes #42", body: "<div>\n<code>\n\nFixes #42", want: "#42"},
		{name: "declaration_after_html_comment", message: "Fixes #42", body: "<div>\n<!--\n\nexample\n\n-->\n</div>\n\nFixes #42"},
		{name: "declaration_after_html_code", message: "Fixes #42", body: "<div>\n<CoDe class=\"example\">\n\nexample\n\n</CoDe>\n</div>\n\nFixes #42"},
		{name: "html_comment_in_raw_code", message: "Fixes #42", body: "<pre>\n<!-- comment -->\n\nexample\n\n</pre>\n\nFixes #42"},
		{name: "html_code_nested", message: "Fixes #42", body: "<code>\n<code>\n</code>\n\nFixes #42\n\n</code>", want: "#42"},
		{name: "html_code_nested_pre", message: "Fixes #42", body: "<code>\n<pre>\n</code>\n\nFixes #42\n\n</pre>", want: "#42"},
		{name: "html_code_closer_in_comment", message: "Fixes #42", body: "<pre>\n<!--\n</pre>\n\nFixes #42\n\n-->\n</pre>", want: "#42"},
		{name: "html_code_closer_in_attribute", message: "Fixes #42", body: "<pre title=\"</pre>\">\n\nFixes #42\n\n</pre>", want: "#42"},
		{name: "html_code_closer_in_multiline_attribute", message: "Fixes #42", body: "<code title=\"\n</code>\n\nFixes #42\n\n\">\n</code>", want: "#42"},
		{name: "html_div_multiline_attribute", message: "Fixes #42", body: "<div title=\"\n\nFixes #42\n\n\">\nvisible\n</div>", want: "#42"},
		{name: "html_details_multiline_attribute", message: "Fixes #42", body: "<details title=\"\n\nFixes #42\n\n\">\nvisible\n</details>", want: "#42"},
		{name: "html_div_single_quoted_attribute", message: "Fixes #42", body: "<div title='\n\nFixes #42\n\n'>\n</div>", want: "#42"},
		{name: "html_details_single_quoted_attribute", message: "Fixes #42", body: "<details title='\n\nFixes #42\n\n'>\n</details>", want: "#42"},
		{name: "html_div_attribute_contains_gt", message: "Fixes #42", body: "<div title=\">\n\nFixes #42\n\n\">\n</div>", want: "#42"},
		{name: "html_inline_unfinished_tag", message: "Fixes #42", body: "Example: <span title=\"\n\nFixes #42\n\n\">\n</span>", want: "#42"},
		{name: "html_unfinished_tag_name", message: "Fixes #42", body: "<div\n\nFixes #42", want: "#42"},
		{name: "html_unfinished_unquoted_attribute", message: "Fixes #42", body: "<details title=\n\nFixes #42", want: "#42"},
		{name: "declaration_after_multiline_tag_conservative", message: "Fixes #42", body: "<div title=\"\nexample\">\n</div>\n\nFixes #42", want: "#42"},
		{name: "declaration_before_unfinished_tag", message: "Fixes #42", body: "Fixes #42\n\n<div title=\"\nexample"},
		{name: "unfinished_tag_cannot_declare_later_reference", message: "Fixes #42\nFixes #43", body: "Fixes #42\n\n<details title=\"\n\nFixes #43\n\n\">\n</details>", want: "#43"},
		{name: "declaration_after_complete_ordinary_tag", message: "Fixes #42", body: "<div title=\"example > text\">\nvisible\n</div>\n\nFixes #42"},
		{name: "declaration_after_complete_details_tag", message: "Fixes #42", body: "<details title='example'>\nvisible\n</details>\n\nFixes #42"},
		{name: "declaration_after_html_attribute", message: "Fixes #42", body: "<code title=\"</code>\">\nexample\n</code>\n\nFixes #42"},
		{name: "declaration_after_inline_html_code", message: "Fixes #42", body: "An example: <code>\nexample\n</code>\n\nFixes #42"},
		{name: "inline_html_code_with_blank", message: "Fixes #42", body: "An example: <code>\n\nFixes #42\n\n</code>", want: "#42"},
		{name: "declaration_after_nested_html_code", message: "Fixes #42", body: "<code><code>example</code></code>\n\nFixes #42"},
		{name: "declaration_after_html_code_spaced_close", message: "Fixes #42", body: "<CODE>\nexample\n</CODE >\n\nFixes #42"},
		{name: "real_declaration_after_code", message: "Fixes #42", body: "~~~text\nFixes #99\n~~~\n<!-- explanation -->\n\nFixes #42"},
		{name: "comment_inside_fence", message: "Fixes #42", body: "```text\n<!--\n```\n\nFixes #42"},
		{name: "link_title_definition_double", message: "Fixes #42", body: "[hidden]: https://example.invalid \"\nFixes #42\n\"", want: "#42"},
		{name: "link_title_definition_double_blank", message: "Fixes #42", body: "[hidden]: https://example.invalid \"\n\nFixes #42\n\n\"", want: "#42"},
		{name: "link_title_definition_double_unfinished", message: "Fixes #42", body: "[hidden]: https://example.invalid \"\nFixes #42", want: "#42"},
		{name: "link_title_definition_double_single_line", message: "Fixes #42", body: "[hidden]: https://example.invalid \"title\"\n\nFixes #42"},
		{name: "link_title_inline_double", message: "Fixes #42", body: "[visible](https://example.invalid \"\nFixes #42\n\")", want: "#42"},
		{name: "link_title_inline_double_blank", message: "Fixes #42", body: "[visible](https://example.invalid \"\n\nFixes #42\n\n\")", want: "#42"},
		{name: "link_title_inline_double_unfinished", message: "Fixes #42", body: "[visible](https://example.invalid \"\nFixes #42", want: "#42"},
		{name: "link_title_inline_double_single_line", message: "Fixes #42", body: "[visible](https://example.invalid \"title\")\n\nFixes #42"},
		{name: "link_title_next_line_double", message: "Fixes #42", body: "[hidden]: https://example.invalid\n\"\nFixes #42\n\"", want: "#42"},
		{name: "link_title_definition_single", message: "Fixes #42", body: "[hidden]: https://example.invalid '\nFixes #42\n'", want: "#42"},
		{name: "link_title_definition_single_blank", message: "Fixes #42", body: "[hidden]: https://example.invalid '\n\nFixes #42\n\n'", want: "#42"},
		{name: "link_title_definition_single_unfinished", message: "Fixes #42", body: "[hidden]: https://example.invalid '\nFixes #42", want: "#42"},
		{name: "link_title_definition_single_single_line", message: "Fixes #42", body: "[hidden]: https://example.invalid 'title'\n\nFixes #42"},
		{name: "link_title_inline_single", message: "Fixes #42", body: "[visible](https://example.invalid '\nFixes #42\n')", want: "#42"},
		{name: "link_title_inline_single_blank", message: "Fixes #42", body: "[visible](https://example.invalid '\n\nFixes #42\n\n')", want: "#42"},
		{name: "link_title_inline_single_unfinished", message: "Fixes #42", body: "[visible](https://example.invalid '\nFixes #42", want: "#42"},
		{name: "link_title_inline_single_single_line", message: "Fixes #42", body: "[visible](https://example.invalid 'title')\n\nFixes #42"},
		{name: "link_title_next_line_single", message: "Fixes #42", body: "[hidden]: https://example.invalid\n'\nFixes #42\n'", want: "#42"},
		{name: "link_title_definition_paren", message: "Fixes #42", body: "[hidden]: https://example.invalid (\nFixes #42\n)", want: "#42"},
		{name: "link_title_definition_paren_blank", message: "Fixes #42", body: "[hidden]: https://example.invalid (\n\nFixes #42\n\n)", want: "#42"},
		{name: "link_title_definition_paren_unfinished", message: "Fixes #42", body: "[hidden]: https://example.invalid (\nFixes #42", want: "#42"},
		{name: "link_title_definition_paren_single_line", message: "Fixes #42", body: "[hidden]: https://example.invalid (title)\n\nFixes #42"},
		{name: "link_title_inline_paren", message: "Fixes #42", body: "[visible](https://example.invalid (\nFixes #42\n))", want: "#42"},
		{name: "link_title_inline_paren_blank", message: "Fixes #42", body: "[visible](https://example.invalid (\n\nFixes #42\n\n))", want: "#42"},
		{name: "link_title_inline_paren_unfinished", message: "Fixes #42", body: "[visible](https://example.invalid (\nFixes #42", want: "#42"},
		{name: "link_title_inline_paren_single_line", message: "Fixes #42", body: "[visible](https://example.invalid (title))\n\nFixes #42"},
		{name: "link_title_next_line_paren", message: "Fixes #42", body: "[hidden]: https://example.invalid\n(\nFixes #42\n)", want: "#42"},
		{name: "link_title_before_ambiguity", message: "Fixes #42", body: "Fixes #42\n\n[hidden]: https://example.invalid \"\nunfinished"},
		{name: "link_title_later_reference", message: "Fixes #42\nFixes #43", body: "Fixes #42\n\n[hidden]: https://example.invalid \"\nFixes #43\n\"", want: "#43"},
		{name: "link_title_closed_multiline_still_stops", message: "Fixes #42", body: "[visible](https://example.invalid \"\ntitle\")\n\nFixes #42", want: "#42"},
		{name: "link_destination_next_line", message: "Fixes #42", body: "[hidden]:\nhttps://example.invalid \"\nFixes #42\n\"", want: "#42"},
		{name: "link_label_next_line", message: "Fixes #42", body: "[hidden\nlabel]: https://example.invalid \"\nFixes #42\n\"", want: "#42"},
		{name: "link_inline_destination_next_line", message: "Fixes #42", body: "[visible](\nhttps://example.invalid \"\nFixes #42\n\")", want: "#42"},
		{name: "link_angle_destination", message: "Fixes #42", body: "[visible](<https://example.invalid> \"title\")\n\nFixes #42"},
		{name: "link_without_title", message: "Fixes #42", body: "[visible](https://example.invalid)\n\nFixes #42"},
		{name: "link_multiple_complete", message: "Fixes #42", body: "[a](https://example.invalid \"one\") [b](https://example.invalid 'two')\n\nFixes #42"},
		{name: "link_after_complete_link", message: "Fixes #42", body: "[a](https://example.invalid \"one\") [b](https://example.invalid \"\nFixes #42\n\")", want: "#42"},
		{name: "link_in_code_ignored", message: "Fixes #42", body: "`[visible](https://example.invalid \"`\n\nFixes #42"},
		{name: "link_in_comment_ignored", message: "Fixes #42", body: "<!-- [hidden]: https://example.invalid \" -->\n\nFixes #42"},
		{name: "escaped_bracket_ignored", message: "Fixes #42", body: "\\[visible](https://example.invalid \"\n\nFixes #42"},
		{name: "link_label_code_cannot_hide_span", message: "Fixes #42", body: "[visible`text](https://example.invalid)\nFixes #42\n`", want: "#42"},
		{name: "link_label_html_cannot_hide_code", message: "Fixes #42", body: "[<code>visible](https://example.invalid)\n\nFixes #42\n</code>", want: "#42"},
		{name: "short_label_cannot_hide_span", message: "Fixes #42", body: "[visible`text]\nFixes #42\n`", want: "#42"},
		{name: "definition_quoted_destination_is_not_title", message: "Fixes #42", body: "[hidden]: \"destination\"\n\"\nFixes #42\n\"", want: "#42"},
		{name: "all_keywords", message: "close #1\ncloses #2\nclosed #3\nfix #4\nfixes #5\nfixed #6\nresolve #7\nresolves #8\nresolved #9", body: "close #1\ncloses #2\nclosed #3\nfix #4\nfixes #5\nfixed #6\nresolve #7\nresolves #8\nresolved #9"},
		{name: "uppercase_colon", message: "CLOSES: #42", body: "Resolves: IVANARAMA/ONEBASE#42"},
		{name: "space_before_colon", message: "Closes : #42", body: "Fixes : #42"},
		{name: "explicit_same_repo", message: "Fixes ivanarama/onebase#42", body: "Fixes #42"},
		{name: "other_repo_is_distinct", message: "Fixes someone/other#42", body: "Fixes #42", want: "someone/other#42"},
		{name: "other_repo_declared", message: "Fixes someone/other#42", body: "Fixes someone/other#42"},
		{name: "url", message: "Fixes https://github.com/someone/other/issues/42", body: "Fixes someone/other#42"},
		{name: "url_is_distinct", message: "Fixes https://github.com/someone/other/issues/42", body: "Fixes #42", want: "someone/other#42"},
		{name: "multiple_references", message: "Resolves #10, resolves #123, resolves octo-org/octo-repo#100", body: "Resolves #10\nResolves #123", want: "octo-org/octo-repo#100"},
		{name: "word_boundary", message: "prefixes #42\nunclosed #42", body: ""},
		{name: "shell_text_is_data", message: "Fixes #42\n$(touch should-not-exist)", body: "Fixes #42\n$(touch should-not-exist)"},
		{name: "base_sync_excludes_main", message: "docs: help", body: "", baseSync: true},
		{name: "base_sync_still_checks_own_commits", message: "Fixes #42", body: "", baseSync: true, want: "#42"},
		{name: "missing_body", message: "docs: help", missingBody: true, want: "body is missing"},
		{name: "missing_base", message: "docs: help", missingBase: true, want: "git cat-file"},
		{name: "shallow_history", message: "docs: help", shallow: true, want: "complete history required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...) //nolint:gosec // fixed executable; test-owned arguments
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			git("init", "-b", "main")
			git("commit", "--allow-empty", "-m", "base")
			base := git("rev-parse", "HEAD")
			git("checkout", "-b", "topic")
			// A docs-only change still has to pass the gate.
			if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("documentation\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			git("add", "README.md")
			git("commit", "-m", tc.message)
			own := git("rev-parse", "HEAD")
			if tc.baseSync {
				git("checkout", "main")
				git("commit", "--allow-empty", "-m", "main change\n\nFixes #999")
				base = git("rev-parse", "HEAD")
				git("checkout", "topic")
				git("merge", "--no-ff", "--no-edit", "main")
			}
			head := git("rev-parse", "HEAD")
			if tc.shallow {
				clone := filepath.Join(t.TempDir(), "clone")
				git("clone", "--depth=1", "--branch=topic", "file://"+filepath.ToSlash(dir), clone)
				dir = clone
			}
			if tc.missingBase {
				base = strings.Repeat("1", 40)
			}
			pr := map[string]any{"body": tc.body, "base": map[string]any{"sha": base, "repo": map[string]any{"full_name": "ivanarama/onebase"}}, "head": map[string]any{"sha": head}}
			if tc.missingBody {
				delete(pr, "body")
			}
			raw, err := json.Marshal(map[string]any{"pull_request": pr})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "event.json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, "-event", path) //nolint:gosec // built CLI with test-owned path
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected failure: %v\n%s", err, out)
				}
			} else if err == nil || !strings.Contains(string(out), tc.want) || !strings.Contains(string(out), "prclosecheck:") {
				t.Fatalf("want failure containing %q, got %v\n%s", tc.want, err, out)
			}
			if tc.want != "" && !tc.missingBody && !tc.missingBase && !tc.shallow && !strings.Contains(string(out), own) {
				t.Fatalf("diagnostic must identify the exact offending commit %s: %s", own, out)
			}
			if tc.baseSync && strings.Contains(string(out), "#999") {
				t.Fatalf("commit from main was incorrectly scanned: %s", out)
			}
			if _, err := os.Stat(filepath.Join(dir, "should-not-exist")); !os.IsNotExist(err) {
				t.Fatal("untrusted shell text executed")
			}
		})
	}
}
