import 'package:flutter/material.dart';

class PanelCard extends StatelessWidget {
  final Widget? title;
  final Widget? trailing;
  final Widget child;
  final EdgeInsetsGeometry padding;
  final VoidCallback? onTap;
  final VoidCallback? onLongPress;

  const PanelCard({
    super.key,
    this.title,
    this.trailing,
    required this.child,
    this.padding = const EdgeInsets.all(22),
    this.onTap,
    this.onLongPress,
  });

  @override
  Widget build(BuildContext context) {
    final content = Padding(
      padding: padding,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (title != null || trailing != null)
            Row(
              crossAxisAlignment: CrossAxisAlignment.center,
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                if (title != null)
                  Expanded(
                    child: DefaultTextStyle.merge(
                      style: const TextStyle(
                        fontSize: 16,
                        fontWeight: FontWeight.w700,
                      ),
                      child: title!,
                    ),
                  )
                else
                  const SizedBox.shrink(),
                if (trailing != null) ...[
                  if (title != null) const SizedBox(width: 8),
                  trailing!,
                ],
              ],
            ),
          if (title != null || trailing != null) const SizedBox(height: 18),
          child,
        ],
      ),
    );

    return Card(
      clipBehavior: Clip.antiAlias,
      child: (onTap == null && onLongPress == null)
          ? content
          : InkWell(
              onTap: onTap,
              onLongPress: onLongPress,
              child: content,
            ),
    );
  }
}
