package runner

import "strings"

// maskInlineDataFields masks names only on single-write, primitive-only JS
// records used for printing or plain field reads. Calls, mutations, aliasing,
// computed properties and shadowed bindings keep the original capability names.
// This is judgment text only; paths/literals still use the original tokens.
func maskInlineDataFields(code string, toks []inlineTok) string {
	writes, params := map[string]int{}, map[string]int{}
	countInlineWritesAndParams(true, toks, writes, params)
	marks := map[int]bool{}
	for i := 0; i+4 < len(toks); i++ {
		if toks[i].kind != "id" || (toks[i].val != "const" && toks[i].val != "let") ||
			toks[i+1].kind != "id" || toks[i+2].val != "=" || toks[i+3].val != "{" {
			continue
		}
		name := toks[i+1].val
		if writes[name] != 1 || params[name] != 0 {
			continue
		}
		end := matchingCloseBracket(toks, i+3)
		if end >= len(toks) {
			continue
		}
		keys, local := map[string]bool{}, map[int]bool{}
		valid := true
		for k := i + 4; k < end; {
			if k+2 >= end || (toks[k].kind != "id" && toks[k].kind != "str") ||
				toks[k+1].val != ":" || !isInlinePrimitive(toks[k+2]) ||
				strings.HasPrefix(toks[k].val, "__") || toks[k].val == "constructor" || toks[k].val == "prototype" {
				valid = false
				break
			}
			keys[toks[k].val] = true
			if toks[k].kind == "id" {
				local[k] = true
			}
			k += 3
			if k < end {
				if toks[k].val != "," {
					valid = false
					break
				}
				k++
			}
		}
		for k, t := range toks {
			if inlineTemplateUses(t, name) {
				valid = false
			}
			if t.kind != "id" || t.val != name || k == i+1 {
				continue
			}
			if k <= end || (k > 0 && (toks[k-1].val == "." || toks[k-1].val == "delete" || toks[k-1].val == "++" || toks[k-1].val == "--")) {
				valid = false
				break
			}
			if k+2 < len(toks) && toks[k+1].val == "." && keys[toks[k+2].val] {
				if k+3 < len(toks) && (inlineAssignOps[toks[k+3].val] ||
					toks[k+3].val == "(" || toks[k+3].val == "[" || toks[k+3].val == "." ||
					toks[k+3].val == "++" || toks[k+3].val == "--" || toks[k+3].val == "?.") {
					valid = false
					break
				}
				local[k+2] = true
			} else if !isInlineRecordPrint(toks, k) {
				valid = false
				break
			}
		}
		if valid {
			for k := range local {
				marks[k] = true
			}
		}
	}
	var out strings.Builder
	prev := 0
	for i, t := range toks {
		if marks[i] {
			out.WriteString(code[prev:t.start])
			out.WriteString("dataField")
			prev = t.end
		}
	}
	out.WriteString(code[prev:])
	return out.String()
}

func isInlinePrimitive(t inlineTok) bool {
	return t.kind == "str" || t.kind == "num" || (t.kind == "id" && (t.val == "true" || t.val == "false" || t.val == "null"))
}

func isInlineRecordPrint(toks []inlineTok, i int) bool {
	if i < 4 || i+1 >= len(toks) || toks[i-1].val != "(" || toks[i-3].val != "." || toks[i+1].val != ")" {
		return false
	}
	return (toks[i-4].val == "console" && toks[i-2].val == "log") ||
		(toks[i-4].val == "JSON" && toks[i-2].val == "stringify") ||
		(toks[i-4].val == "Object" && (toks[i-2].val == "keys" || toks[i-2].val == "values" || toks[i-2].val == "entries"))
}
