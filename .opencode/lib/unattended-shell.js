// Shell reader for the unattended policy. Mirrors
// internal/runner/approval_unattended_parse.go: it reads one command the way
// the shell will run it, closely enough to see what lands in command position
// after quote removal and expansion, and records which words are dynamic.
// parseUnattendedScript returns null for input it cannot read with certainty.

export const INDIRECT_EXPANSION = "${!}";

const NAME_START = /[A-Za-z_]/;
const NAME_CHAR = /[A-Za-z0-9_]/;

function newScript() {
  return { segs: [], children: [], vars: new Set(), dataOnly: false };
}

function newSeg() {
  return { words: [], pipedIn: false, stdin: "", stdinText: "", stdinDynamic: false };
}

class Parser {
  constructor(s) {
    this.s = s;
    this.i = 0;
    this.out = newScript();
    this.cur = newSeg();
    this.word = "";
    this.inWord = false;
    this.dyn = false;
    this.proc = false;
    this.redir = "";
    this.pipe = false;
    this.pending = [];
    this.depth = 0;
  }

  peek(off) {
    return this.i + off < this.s.length ? this.s[this.i + off] : "";
  }

  addChar(c) {
    this.word += c;
    this.inWord = true;
  }

  endWord() {
    if (!this.inWord) return;
    const w = { text: this.word, dynamic: this.dyn, procSub: this.proc };
    this.word = "";
    this.inWord = this.dyn = this.proc = false;
    if (this.redir === "in") this.cur.stdin = w.procSub ? "procsub" : "file";
    else if (this.redir === "herestring") Object.assign(this.cur, { stdin: "herestring", stdinText: w.text, stdinDynamic: w.dynamic });
    else if (this.redir !== "out") this.cur.words.push(w);
    this.redir = "";
  }

  flush(nextPipe) {
    if (this.cur.words.length > 0 || this.cur.stdin !== "") {
      this.cur.pipedIn = this.pipe;
      this.out.segs.push(this.cur);
    }
    this.cur = newSeg();
    this.pipe = nextPipe;
  }

  // run parses to the end of input or, when nested, the `)` that closes the
  // region; it returns that index, or -1 when the input cannot be read.
  run(nested) {
    const s = this.s;
    while (this.i < s.length) {
      const c = s[this.i];
      if (c === "\\") {
        if (this.i + 1 < s.length) {
          if (s[this.i + 1] !== "\n") this.addChar(s[this.i + 1]);
          this.i += 2;
        } else this.i++;
      } else if (c === "'") {
        const end = s.indexOf("'", this.i + 1);
        if (end < 0) return -1;
        this.word += s.slice(this.i + 1, end);
        this.inWord = true;
        this.i = end + 1;
      } else if (c === '"') {
        this.inWord = true;
        this.i++;
        if (!this.readDouble('"')) return -1;
      } else if (c === "$") {
        if (!this.readDollar()) return -1;
      } else if (c === "`") {
        if (!this.readBacktick()) return -1;
      } else if (c === " " || c === "\t" || c === "\r") {
        this.endWord();
        this.i++;
      } else if (c === "\n") {
        this.endWord();
        this.flush(false);
        this.i++;
        if (!this.readHeredocBodies()) return -1;
      } else if (c === "#" && !this.inWord) {
        while (this.i < s.length && s[this.i] !== "\n") this.i++;
      } else if (c === ";") {
        this.endWord();
        this.flush(false);
        this.i++;
      } else if (c === "&") {
        this.endWord();
        if (this.peek(1) === "&") {
          this.flush(false);
          this.i += 2;
        } else if (this.peek(1) === ">") {
          this.redir = "out";
          this.i += 2;
          if (this.peek(0) === ">") this.i++;
        } else {
          this.flush(false);
          this.i++;
        }
      } else if (c === "|") {
        this.endWord();
        if (this.peek(1) === "|") {
          this.flush(false);
          this.i += 2;
        } else {
          this.flush(true);
          this.i++;
          if (this.peek(0) === "&") this.i++;
        }
      } else if (c === "(") {
        if (this.inWord && this.word.endsWith("=")) {
          if (!this.readNested(this.i + 1, false, true)) return -1;
          this.inWord = this.dyn = true;
          continue;
        }
        this.endWord();
        if (this.cur.words.length === 0 && this.peek(1) === "(") {
          if (!this.readNested(this.i + 2, false, true)) return -1;
          if (this.peek(0) === ")") this.i++;
          continue;
        }
        this.flush(false);
        this.depth++;
        this.i++;
      } else if (c === ")") {
        this.endWord();
        this.flush(false);
        if (nested && this.depth === 0) return this.pending.length === 0 ? this.i : -1;
        if (this.depth > 0) this.depth--;
        this.i++;
      } else if (c === "<" || c === ">") {
        if (!this.readRedirect(c)) return -1;
      } else {
        this.addChar(c);
        this.i++;
      }
    }
    if (nested) return -1;
    this.endWord();
    this.flush(false);
    return this.pending.length === 0 ? s.length : -1;
  }

  // readDouble reads a double-quoted run up to term ("" = end of input, for
  // an expanding heredoc body).
  readDouble(term) {
    const s = this.s;
    while (this.i < s.length) {
      const c = s[this.i];
      if (term !== "" && c === term) {
        this.i++;
        return true;
      }
      if (c === "\\" && this.i + 1 < s.length && "$`\"\\\n".includes(s[this.i + 1])) {
        if (s[this.i + 1] !== "\n") this.word += s[this.i + 1];
        this.i += 2;
      } else if (c === "$") {
        if (!this.readDollar()) return false;
      } else if (c === "`") {
        if (!this.readBacktick()) return false;
      } else {
        this.word += c;
        this.i++;
      }
    }
    return term === "";
  }

  readDollar() {
    const s = this.s;
    const next = this.peek(1);
    if (next === "(") {
      this.inWord = this.dyn = true;
      return this.readNested(this.i + 2, false, false);
    }
    if (next === "{") {
      const end = braceEnd(s, this.i + 2);
      if (end < 0) return false;
      const inner = s.slice(this.i + 2, end);
      if (inner.startsWith("!")) this.out.vars.add(INDIRECT_EXPANSION);
      const name = inner.startsWith("!") ? inner.slice(1) : inner;
      let n = 0;
      while (n < name.length && NAME_CHAR.test(name[n])) n++;
      this.out.vars.add(name.slice(0, n));
      if (inner.includes("$") || inner.includes("`")) {
        const child = parseUnattendedScript(inner);
        if (!child) return false;
        this.out.children.push(child);
      }
      this.inWord = this.dyn = true;
      this.i = end + 1;
      return true;
    }
    if (next === "'") {
      let j = this.i + 2;
      while (j < s.length && s[j] !== "'") {
        if (s[j] === "\\") j++;
        j++;
      }
      if (j >= s.length) return false;
      this.word += s.slice(this.i + 2, j);
      this.inWord = this.dyn = true;
      this.i = j + 1;
      return true;
    }
    if (next === '"') {
      this.inWord = true;
      this.i += 2;
      return this.readDouble('"');
    }
    if (next !== "" && NAME_START.test(next)) {
      let j = this.i + 1;
      while (j < s.length && NAME_CHAR.test(s[j])) j++;
      this.out.vars.add(s.slice(this.i + 1, j));
      this.inWord = this.dyn = true;
      this.i = j;
      return true;
    }
    if (next !== "" && "0123456789@*#?$!-".includes(next)) {
      this.inWord = this.dyn = true;
      this.i += 2;
      return true;
    }
    this.addChar("$");
    this.i++;
    return true;
  }

  readBacktick() {
    const s = this.s;
    let inner = "";
    let j = this.i + 1;
    while (j < s.length && s[j] !== "`") {
      if (s[j] === "\\" && j + 1 < s.length && "`$\\".includes(s[j + 1])) j++;
      inner += s[j];
      j++;
    }
    if (j >= s.length) return false;
    const child = parseUnattendedScript(inner);
    if (!child) return false;
    this.out.children.push(child);
    this.inWord = this.dyn = true;
    this.i = j + 1;
    return true;
  }

  readNested(start, piped, dataOnly) {
    const sub = new Parser(this.s);
    sub.i = start;
    sub.pipe = piped;
    const end = sub.run(true);
    if (end < 0) return false;
    sub.out.dataOnly = dataOnly;
    this.out.children.push(sub.out);
    this.i = end + 1;
    return true;
  }

  readRedirect(c) {
    if (this.inWord && /^[0-9]+$/.test(this.word) && !this.dyn) {
      this.word = "";
      this.inWord = false; // a file descriptor number, as in 2>&1
    }
    this.endWord();
    if (this.peek(1) === "(") {
      if (!this.readNested(this.i + 2, c === ">", false)) return false;
      this.inWord = this.proc = true;
      this.word += "/dev/fd/63";
      return true;
    }
    if (c === "<" && this.peek(1) === "<") {
      if (this.peek(2) === "<") {
        this.redir = "herestring";
        this.i += 3;
        return true;
      }
      return this.readHeredocMarker();
    }
    this.i++;
    if (">&|".includes(this.peek(0)) && this.peek(0) !== "") this.i++;
    this.redir = c === "<" ? "in" : "out";
    return true;
  }

  readHeredocMarker() {
    const s = this.s;
    this.i += 2;
    const h = { seg: this.cur, delim: "", quoted: false, dash: false };
    if (this.peek(0) === "-") {
      h.dash = true;
      this.i++;
    }
    while (this.peek(0) === " " || this.peek(0) === "\t") this.i++;
    let delim = "";
    while (this.i < s.length) {
      const c = s[this.i];
      if (c === "'" || c === '"') {
        const end = s.indexOf(c, this.i + 1);
        if (end < 0) return false;
        delim += s.slice(this.i + 1, end);
        h.quoted = true;
        this.i = end + 1;
        continue;
      }
      if (c === "\\" && this.i + 1 < s.length) {
        delim += s[this.i + 1];
        h.quoted = true;
        this.i += 2;
        continue;
      }
      if (" \t\r\n;&|()<>".includes(c)) break;
      delim += c;
      this.i++;
    }
    h.delim = delim;
    if (delim === "") return false;
    this.pending.push(h);
    return true;
  }

  readHeredocBodies() {
    const s = this.s;
    for (const h of this.pending) {
      const body = [];
      let found = false;
      while (this.i <= s.length) {
        const end = s.indexOf("\n", this.i);
        const line = end >= 0 ? s.slice(this.i, end) : s.slice(this.i);
        this.i = end >= 0 ? end + 1 : s.length + 1;
        const cmp = h.dash ? line.replace(/^\t+/, "") : line;
        if (cmp === h.delim) {
          found = true;
          break;
        }
        body.push(line);
        if (end < 0) break;
      }
      if (!found) return false;
      if (this.i > s.length) this.i = s.length;
      const text = body.join("\n");
      Object.assign(h.seg, { stdin: "heredoc", stdinText: text });
      if (!h.quoted && /[$`]/.test(text)) {
        h.seg.stdinDynamic = true;
        const sub = new Parser(text);
        if (!sub.readDouble("")) return false;
        this.out.children.push(sub.out);
      }
    }
    this.pending = [];
    return true;
  }
}

function braceEnd(s, start) {
  let depth = 0;
  let quote = "";
  for (let i = start; i < s.length; i++) {
    const c = s[i];
    if (quote === "'") {
      if (c === "'") quote = "";
    } else if (c === "\\") i++;
    else if (quote === '"') {
      if (c === '"') quote = "";
    } else if (c === "'" || c === '"') quote = c;
    else if (c === "{") depth++;
    else if (c === "}") {
      if (depth === 0) return i;
      depth--;
    }
  }
  return -1;
}

export function parseUnattendedScript(s) {
  const p = new Parser(String(s ?? ""));
  return p.run(false) < 0 ? null : p.out;
}
