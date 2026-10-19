import 'package:flutter/material.dart';

import '../../models/chat_message.dart';
import 'ai_chat_markdown.dart';

class AiChatMessageItem extends StatelessWidget {
  const AiChatMessageItem({super.key, required this.message});

  final ChatMessage message;

  @override
  Widget build(BuildContext context) {
    if (message.isUser) {
      return Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text(
            '>',
            style: TextStyle(
              color: Colors.blueAccent,
              fontWeight: FontWeight.bold,
              fontSize: 15,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: SelectableText(
              message.text,
              style: const TextStyle(
                color: Colors.white,
                fontSize: 15,
                fontFamily: 'monospace',
              ),
            ),
          ),
        ],
      );
    }

    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: Colors.white.withValues(alpha: 0.05),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: Colors.white.withValues(alpha: 0.1)),
      ),
      child: AiChatMarkdown(text: message.text),
    );
  }
}
