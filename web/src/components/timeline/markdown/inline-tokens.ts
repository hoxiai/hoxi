interface InlineTokenMatch {
  token: string;
  index: number;
  end: number;
}

/**
 * Search paths only at segment boundaries. Putting an unanchored `segment+/`
 * alternative in the syntax regex retries the whole remaining word at every
 * character when a long paragraph or code line contains no slash (quadratic).
 * Keep syntax and path lookahead separately so neither is rescanned for each
 * token of the other kind, and syntax still owns paths inside links/code.
 */
export function* scanInlineTokens(
  text: string,
  syntax: RegExp,
  pathSource: string
): Generator<InlineTokenMatch> {
  const syntaxRegex = new RegExp(syntax.source, syntax.flags);
  // Consume the boundary instead of using lookbehind, for older WebKit hosts.
  const pathRegex = new RegExp(`(?:^|(?=~/)|[^\\w@.+\\-\\u4e00-\\u9fa5/])(${pathSource})`, "gi");
  let cursor = 0;
  let nextSyntax: InlineTokenMatch | undefined;
  let nextPath: InlineTokenMatch | undefined;
  let syntaxDone = false;
  let pathsDone = false;

  while (cursor < text.length) {
    if (nextSyntax && nextSyntax.index < cursor) {
      nextSyntax = undefined;
      syntaxRegex.lastIndex = cursor;
    }
    if (nextPath && nextPath.index < cursor) {
      nextPath = undefined;
      // The preceding boundary may belong to the syntax token just consumed.
      pathRegex.lastIndex = Math.max(0, cursor - 1);
    }
    if (!nextSyntax && !syntaxDone) {
      const match = syntaxRegex.exec(text);
      if (match) {
        nextSyntax = { token: match[0], index: match.index, end: syntaxRegex.lastIndex };
      } else syntaxDone = true;
    }
    if (!nextPath && !pathsDone) {
      const match = pathRegex.exec(text);
      if (match) {
        nextPath = {
          token: match[1],
          index: pathRegex.lastIndex - match[1].length,
          end: pathRegex.lastIndex,
        };
      } else pathsDone = true;
    }
    if (!nextSyntax && !nextPath) return;
    const takeSyntax = nextSyntax && (!nextPath || nextSyntax.index <= nextPath.index);
    const next = (takeSyntax ? nextSyntax : nextPath)!;
    if (takeSyntax) nextSyntax = undefined;
    else nextPath = undefined;
    cursor = next.end;
    yield next;
  }
}
