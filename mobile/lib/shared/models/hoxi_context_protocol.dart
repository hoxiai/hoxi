// Pure Dart codec & parser for the hoxi-context v1 structured context protocol.
// Mirrors web/src/store/context-protocol.ts so desktop and mobile share identical
// parsing, fallback, title extraction, and clipboard formatting semantics.
import 'dart:convert';

enum HoxiContextKind { quote, entity, record, snapshot }

class HoxiContextResolveSpec {
  const HoxiContextResolveSpec({
    this.servicePath,
    this.mcpTool,
    this.mcpArgs,
  });

  final String? servicePath;
  final String? mcpTool;
  final Map<String, Object?>? mcpArgs;

  String get displayLabel {
    if (mcpTool != null && mcpTool!.isNotEmpty) return mcpTool!;
    if (servicePath != null && servicePath!.isNotEmpty) return servicePath!;
    return '';
  }
}

class HoxiContextRef {
  const HoxiContextRef({
    required this.id,
    this.uri,
    this.resolve,
  });

  final String id;
  final String? uri;
  final HoxiContextResolveSpec? resolve;
}

class HoxiContextItem {
  const HoxiContextItem({
    required this.kind,
    this.title,
    this.subtitle,
    this.badge,
    this.entityType,
    this.ref,
    this.content,
  });

  final HoxiContextKind kind;
  final String? title;
  final String? subtitle;
  final String? badge;
  final String? entityType;
  final HoxiContextRef? ref;
  final String? content;

  String get bodyText => (content ?? '').trim();
}

class HoxiContextSource {
  const HoxiContextSource({
    this.appId,
    this.appName,
    this.tabId,
    this.tabTitle,
  });

  final String? appId;
  final String? appName;
  final String? tabId;
  final String? tabTitle;

  String get displayLabel {
    final name = (appName ?? '').trim();
    if (name.isNotEmpty) return name;
    return (appId ?? '').trim();
  }
}

class HoxiContextEnvelope {
  const HoxiContextEnvelope({
    this.version = 1,
    this.source,
    required this.items,
  });

  final int version;
  final HoxiContextSource? source;
  final List<HoxiContextItem> items;
}

class ParsedHoxiContextPrompt {
  const ParsedHoxiContextPrompt({
    required this.envelopes,
    required this.bodyText,
    required this.hasContext,
  });

  final List<HoxiContextEnvelope> envelopes;
  final String bodyText;
  final bool hasContext;
}

const _contextOpenTag = '<hoxi-context';
const _contextCloseTag = '</hoxi-context>';
const _itemOpenTag = '<item';
const _itemCloseTag = '</item>';
const _cdataStart = '<![CDATA[';
const _cdataEnd = ']]>';

final _xmlEntityRe = RegExp(r'&(quot|lt|gt|amp|#10|#13|#9);');
final _whitespaceCharRe = RegExp(r'\s');
final _attrKeyCharRe = RegExp(r'[A-Za-z0-9_:-]');

String? _cleanStr(String? raw, [int maxLen = 512]) {
  if (raw == null) return null;
  final trimmed = raw.trim();
  if (trimmed.isEmpty) return null;
  return trimmed.length > maxLen ? trimmed.substring(0, maxLen) : trimmed;
}

String _unescapeXmlAttr(String value) {
  if (!value.contains('&')) return value;
  return value.replaceAllMapped(_xmlEntityRe, (match) {
    return switch (match.group(1)) {
      'quot' => '"',
      'lt' => '<',
      'gt' => '>',
      'amp' => '&',
      '#10' => '\n',
      '#13' => '\r',
      '#9' => '\t',
      _ => match.group(0)!,
    };
  });
}

Object? _tryParseJsonStructure(String raw) {
  final trimmed = raw.trim();
  if (trimmed.length < 2) return null;
  final first = trimmed[0];
  final last = trimmed[trimmed.length - 1];
  if (!((first == '{' && last == '}') || (first == '[' && last == ']'))) {
    return null;
  }
  try {
    final decoded = jsonDecode(trimmed);
    if (decoded is Map || decoded is List) return decoded;
  } catch (_) {
    return null;
  }
  return null;
}

HoxiContextKind _inferKind(
  String? rawKind,
  bool hasRefOrEntity,
  bool hasTitle,
  String bodyText,
) {
  switch (rawKind) {
    case 'quote':
      return HoxiContextKind.quote;
    case 'entity':
      return HoxiContextKind.entity;
    case 'record':
      return HoxiContextKind.record;
    case 'snapshot':
      return HoxiContextKind.snapshot;
  }
  if (hasRefOrEntity) return HoxiContextKind.entity;
  if (_tryParseJsonStructure(bodyText) != null) return HoxiContextKind.record;
  return hasTitle ? HoxiContextKind.record : HoxiContextKind.quote;
}

Map<String, String> _parseXmlAttrs(String rawAttrs) {
  final attrs = <String, String>{};
  var i = 0;
  final n = rawAttrs.length;
  while (i < n) {
    while (i < n && _whitespaceCharRe.hasMatch(rawAttrs[i])) {
      i++;
    }
    if (i >= n) break;
    final keyStart = i;
    while (i < n && _attrKeyCharRe.hasMatch(rawAttrs[i])) {
      i++;
    }
    final key = rawAttrs.substring(keyStart, i);
    while (i < n && _whitespaceCharRe.hasMatch(rawAttrs[i])) {
      i++;
    }
    if (key.isEmpty || i >= n || rawAttrs[i] != '=') {
      while (i < n && !_whitespaceCharRe.hasMatch(rawAttrs[i])) {
        i++;
      }
      continue;
    }
    i++; // skip '='
    while (i < n && _whitespaceCharRe.hasMatch(rawAttrs[i])) {
      i++;
    }
    if (i >= n) break;
    final quote = rawAttrs[i];
    if (quote != '"' && quote != "'") {
      final valStart = i;
      while (i < n && !_whitespaceCharRe.hasMatch(rawAttrs[i])) {
        i++;
      }
      attrs[key] = _unescapeXmlAttr(rawAttrs.substring(valStart, i));
      continue;
    }
    i++; // skip opening quote
    final valStart = i;
    while (i < n && rawAttrs[i] != quote) {
      i++;
    }
    attrs[key] = _unescapeXmlAttr(rawAttrs.substring(valStart, i));
    if (i < n && rawAttrs[i] == quote) i++;
  }
  return attrs;
}

int _findTagEnd(String src, int startIndex) {
  String? quote;
  for (var i = startIndex; i < src.length; i++) {
    final ch = src[i];
    if (quote != null) {
      if (ch == quote) quote = null;
    } else if (ch == '"' || ch == "'") {
      quote = ch;
    } else if (ch == '>') {
      return i;
    }
  }
  return -1;
}

String _decodeCdataSegments(String rawBody) {
  var trimmed = rawBody;
  if (trimmed.startsWith('\n')) trimmed = trimmed.substring(1);
  if (trimmed.endsWith('\n  ')) {
    trimmed = trimmed.substring(0, trimmed.length - 3);
  } else if (trimmed.endsWith('\n')) {
    trimmed = trimmed.substring(0, trimmed.length - 1);
  }

  if (!trimmed.startsWith(_cdataStart)) return trimmed;
  final out = StringBuffer();
  var pos = 0;
  while (pos < trimmed.length) {
    if (!trimmed.startsWith(_cdataStart, pos)) return trimmed;
    final endIdx = trimmed.indexOf(_cdataEnd, pos + _cdataStart.length);
    if (endIdx < 0) return trimmed;
    out.write(trimmed.substring(pos + _cdataStart.length, endIdx));
    pos = endIdx + _cdataEnd.length;
  }
  return out.toString();
}

int _findCloseOutsideCdata(String src, String closeTag, int fromIndex) {
  var pos = fromIndex;
  while (pos < src.length) {
    final nextClose = src.indexOf(closeTag, pos);
    if (nextClose < 0) return -1;
    final nextCdata = src.indexOf(_cdataStart, pos);
    if (nextCdata >= 0 && nextCdata < nextClose) {
      final cdataEnd = src.indexOf(_cdataEnd, nextCdata + _cdataStart.length);
      if (cdataEnd < 0) return -1;
      pos = cdataEnd + _cdataEnd.length;
      continue;
    }
    return nextClose;
  }
  return -1;
}

List<HoxiContextItem> _parseEnvelopeInnerItems(String inner) {
  final items = <HoxiContextItem>[];
  var pos = 0;
  while (pos < inner.length) {
    final itemStart = inner.indexOf(_itemOpenTag, pos);
    if (itemStart < 0) break;
    final afterTagName = itemStart + _itemOpenTag.length;
    if (afterTagName < inner.length) {
      final nextChar = inner[afterTagName];
      if (!_whitespaceCharRe.hasMatch(nextChar) &&
          nextChar != '/' &&
          nextChar != '>') {
        pos = afterTagName;
        continue;
      }
    }
    final tagEnd = _findTagEnd(inner, afterTagName);
    if (tagEnd < 0) break;
    final selfClosing = tagEnd > afterTagName && inner[tagEnd - 1] == '/';
    final attrSlice = inner.substring(
      afterTagName,
      selfClosing ? tagEnd - 1 : tagEnd,
    );
    final attrs = _parseXmlAttrs(attrSlice);

    var body = '';
    if (selfClosing) {
      pos = tagEnd + 1;
    } else {
      final closeIdx = _findCloseOutsideCdata(inner, _itemCloseTag, tagEnd + 1);
      if (closeIdx < 0) break;
      body = _decodeCdataSegments(inner.substring(tagEnd + 1, closeIdx));
      pos = closeIdx + _itemCloseTag.length;
    }

    Map<String, Object?>? resolveArgs;
    final rawResolveArgs = attrs['resolve-args'];
    if (rawResolveArgs != null && rawResolveArgs.isNotEmpty) {
      final decoded = _tryParseJsonStructure(rawResolveArgs);
      if (decoded is Map) {
        resolveArgs = decoded.map((k, v) => MapEntry(k.toString(), v));
      }
    }

    final resolveService = _cleanStr(attrs['resolve-service']);
    final resolveTool = _cleanStr(attrs['resolve-tool'], 160);
    final resolve = (resolveService != null || resolveTool != null)
        ? HoxiContextResolveSpec(
            servicePath: resolveService,
            mcpTool: resolveTool,
            mcpArgs: resolveTool != null ? resolveArgs : null,
          )
        : null;

    final id = _cleanStr(attrs['id'], 256);
    final uri = _cleanStr(attrs['uri']);
    final ref = (id != null || uri != null || resolve != null)
        ? HoxiContextRef(
            id: id ?? uri ?? '',
            uri: uri,
            resolve: resolve,
          )
        : null;

    final title = _cleanStr(attrs['title'], 240);
    final subtitle = _cleanStr(attrs['subtitle'], 320);
    final badge = _cleanStr(attrs['badge'], 64);
    final entityType = _cleanStr(attrs['type'], 64);
    final cleanBody = body.isEmpty ? null : body;

    final kind = _inferKind(
      attrs['kind'],
      (ref?.id.isNotEmpty ?? false) || entityType != null,
      title != null,
      body,
    );

    if (title != null ||
        subtitle != null ||
        cleanBody != null ||
        (ref?.id.isNotEmpty ?? false) ||
        (ref?.uri?.isNotEmpty ?? false)) {
      items.add(
        HoxiContextItem(
          kind: kind,
          title: title,
          subtitle: subtitle,
          badge: badge,
          entityType: entityType,
          ref: ref,
          content: cleanBody,
        ),
      );
    }
  }
  return items;
}

HoxiContextSource? _normalizeSource(Map<String, String> attrs) {
  final appId = _cleanStr(attrs['source'], 128);
  final appName = _cleanStr(attrs['source-name'], 128);
  final tabId = _cleanStr(attrs['tab'], 128);
  final tabTitle = _cleanStr(attrs['tab-title'], 128);
  if (appId == null && appName == null && tabId == null && tabTitle == null) {
    return null;
  }
  return HoxiContextSource(
    appId: appId,
    appName: appName,
    tabId: tabId,
    tabTitle: tabTitle,
  );
}

ParsedHoxiContextPrompt parseHoxiContextPrompt(String raw) {
  if (raw.isEmpty || !raw.contains(_contextOpenTag)) {
    return ParsedHoxiContextPrompt(
      envelopes: const [],
      bodyText: raw,
      hasContext: false,
    );
  }
  final envelopes = <HoxiContextEnvelope>[];
  final textSegments = <String>[];
  var cursor = 0;

  while (cursor < raw.length) {
    final startIdx = raw.indexOf(_contextOpenTag, cursor);
    if (startIdx < 0) {
      textSegments.add(raw.substring(cursor));
      break;
    }
    final afterName = startIdx + _contextOpenTag.length;
    if (afterName < raw.length) {
      final nextCh = raw[afterName];
      if (!_whitespaceCharRe.hasMatch(nextCh) && nextCh != '>') {
        textSegments.add(raw.substring(cursor, afterName));
        cursor = afterName;
        continue;
      }
    }
    final openEnd = _findTagEnd(raw, afterName);
    if (openEnd < 0) {
      textSegments.add(raw.substring(cursor));
      break;
    }
    final closeIdx = _findCloseOutsideCdata(raw, _contextCloseTag, openEnd + 1);
    if (closeIdx < 0) {
      textSegments.add(raw.substring(cursor));
      break;
    }
    final attrs = _parseXmlAttrs(raw.substring(afterName, openEnd));
    final inner = raw.substring(openEnd + 1, closeIdx);
    final items = _parseEnvelopeInnerItems(inner);
    if (items.isEmpty) {
      textSegments.add(raw.substring(cursor, closeIdx + _contextCloseTag.length));
      cursor = closeIdx + _contextCloseTag.length;
      continue;
    }
    if (startIdx > cursor) {
      textSegments.add(raw.substring(cursor, startIdx));
    }
    envelopes.add(
      HoxiContextEnvelope(
        version: 1,
        source: _normalizeSource(attrs),
        items: items,
      ),
    );
    cursor = closeIdx + _contextCloseTag.length;
  }

  if (envelopes.isEmpty) {
    return ParsedHoxiContextPrompt(
      envelopes: const [],
      bodyText: raw,
      hasContext: false,
    );
  }
  final bodyText = textSegments
      .map((s) => s.trim())
      .where((s) => s.isNotEmpty)
      .join('\n\n');
  return ParsedHoxiContextPrompt(
    envelopes: envelopes,
    bodyText: bodyText,
    hasContext: true,
  );
}

String extractContextTitleFallback(String rawPrompt) {
  final parsed = parseHoxiContextPrompt(rawPrompt);
  if (!parsed.hasContext) return rawPrompt;
  if (parsed.bodyText.trim().isNotEmpty) return parsed.bodyText;
  if (parsed.envelopes.isEmpty || parsed.envelopes.first.items.isEmpty) {
    return rawPrompt;
  }
  final first = parsed.envelopes.first.items.first;
  return first.title ??
      first.subtitle ??
      (first.bodyText.isNotEmpty ? first.bodyText : rawPrompt);
}

int estimateContextEnvelopeChars(HoxiContextEnvelope envelope) {
  var total = 0;
  for (final item in envelope.items) {
    total += (item.title?.length ?? 0) +
        (item.subtitle?.length ?? 0) +
        item.bodyText.length;
  }
  return total;
}

String formatContextSizeLabel(int chars) {
  if (chars < 1000) return '$chars B';
  return '${(chars / 1024).toStringAsFixed(1)} KB';
}

String deriveContextItemPreview(HoxiContextItem item, {int maxLen = 84}) {
  if (item.subtitle != null && item.subtitle!.isNotEmpty) return item.subtitle!;
  final body = item.bodyText.replaceAll(RegExp(r'\s+'), ' ').trim();
  if (body.isEmpty) return item.ref?.uri ?? item.ref?.id ?? '';
  return body.length > maxLen ? '${body.substring(0, maxLen)}…' : body;
}

({String text, bool isJson}) prettyFormatContextBody(String raw) {
  final decoded = _tryParseJsonStructure(raw);
  if (decoded != null) {
    try {
      const encoder = JsonEncoder.withIndent('  ');
      return (text: encoder.convert(decoded), isJson: true);
    } catch (_) {
      return (text: raw, isJson: false);
    }
  }
  return (text: raw, isJson: false);
}

String formatContextPromptForClipboard(String raw) {
  final parsed = parseHoxiContextPrompt(raw);
  if (!parsed.hasContext) return raw;
  final sections = <String>[];
  for (final env in parsed.envelopes) {
    final src = env.source?.displayLabel ?? '';
    final itemLines = <String>[];
    for (final item in env.items) {
      final head = [
        if (src.isNotEmpty) '[$src]',
        if (item.title != null) item.title!,
        if (item.badge != null) '(${item.badge!})',
        if (item.subtitle != null) item.subtitle!,
      ].join(' ');
      final body = item.bodyText;
      if (body.isEmpty) {
        if (head.isNotEmpty) itemLines.add(head);
      } else {
        itemLines.add(head.isNotEmpty ? '$head\n$body' : body);
      }
    }
    if (itemLines.isNotEmpty) sections.add(itemLines.join('\n\n'));
  }
  if (parsed.bodyText.isNotEmpty) sections.add(parsed.bodyText);
  return sections.join('\n\n');
}
