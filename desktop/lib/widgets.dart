import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'core.dart';
import 'state.dart';

class SectionCard extends StatelessWidget {
  const SectionCard({super.key, required this.child, this.padding = 24});
  final Widget child;
  final double padding;
  @override
  Widget build(BuildContext context) => Card(
    color: Theme.of(context).colorScheme.surfaceContainerLow,
    child: Padding(padding: EdgeInsets.all(padding), child: child),
  );
}

class PageHeading extends StatelessWidget {
  const PageHeading(this.title, this.subtitle, {super.key, this.action});
  final String title, subtitle;
  final Widget? action;
  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(bottom: 28),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                title,
                style: Theme.of(context).textTheme.headlineMedium
                    ?.copyWith(fontWeight: FontWeight.w600),
              ),
              const SizedBox(height: 10),
              Text(
                subtitle,
                style: TextStyle(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                  height: 1.6,
                ),
              ),
            ],
          ),
        ),
        if (action != null) ...[const SizedBox(width: 20), action!],
      ],
    ),
  );
}

class StatusPill extends StatelessWidget {
  const StatusPill(
    this.label, {
    super.key,
    this.active = false,
    this.error = false,
  });
  final String label;
  final bool active, error;
  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).colorScheme;
    final color = error
        ? c.error
        : active
        ? c.primary
        : c.onSurfaceVariant;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
      decoration: BoxDecoration(
        color: error
            ? c.errorContainer
            : active
            ? c.primaryContainer
            : c.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(30),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.circle, size: 7, color: color),
          const SizedBox(width: 7),
          Flexible(
            child: Text(
              label,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                color: color,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class Notice extends StatelessWidget {
  const Notice(this.text, {super.key, this.icon = Icons.info_outline});
  final String text;
  final IconData icon;
  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 12),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(icon, size: 20, color: Theme.of(context).colorScheme.primary),
        const SizedBox(width: 12),
        Expanded(
          child: Text(
            text,
            style: TextStyle(
              height: 1.6,
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          ),
        ),
      ],
    ),
  );
}

class EmptyState extends StatelessWidget {
  const EmptyState(
    this.title,
    this.detail, {
    super.key,
    this.action,
    this.icon = Icons.hub_outlined,
  });
  final String title, detail;
  final Widget? action;
  final IconData icon;
  @override
  Widget build(BuildContext context) => SectionCard(
    child: Center(
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 40),
        child: Column(
          children: [
            Icon(icon, size: 46, color: Theme.of(context).colorScheme.primary),
            const SizedBox(height: 20),
            Text(title, style: Theme.of(context).textTheme.titleLarge),
            const SizedBox(height: 12),
            Text(
              detail,
              textAlign: TextAlign.center,
              style: TextStyle(
                height: 1.7,
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
            ),
            if (action != null) ...[const SizedBox(height: 24), action!],
          ],
        ),
      ),
    ),
  );
}

class AsyncButton extends StatefulWidget {
  const AsyncButton({
    super.key,
    required this.label,
    required this.onPressed,
    this.icon = Icons.check,
    this.filled = false,
  });
  final String label;
  final Future<void> Function()? onPressed;
  final IconData icon;
  final bool filled;
  @override
  State<AsyncButton> createState() => _AsyncButtonState();
}

class _AsyncButtonState extends State<AsyncButton> {
  bool busy = false;
  @override
  Widget build(BuildContext context) {
    final icon = busy
        ? const SizedBox(
            width: 16,
            height: 16,
            child: CircularProgressIndicator(strokeWidth: 2),
          )
        : Icon(widget.icon, size: 18);
    Future<void> press() async {
      setState(() => busy = true);
      try {
        await widget.onPressed!();
      } finally {
        if (mounted) setState(() => busy = false);
      }
    }

    final callback = busy || widget.onPressed == null ? null : press;
    return widget.filled
        ? FilledButton.icon(
            onPressed: callback,
            icon: icon,
            label: Text(widget.label),
          )
        : OutlinedButton.icon(
            onPressed: callback,
            icon: icon,
            label: Text(widget.label),
          );
  }
}

void toast(BuildContext context, String message) {
  ScaffoldMessenger.of(context).showSnackBar(
    SnackBar(content: Text(message), behavior: SnackBarBehavior.floating),
  );
}

Future<dynamic> command(
  BuildContext context,
  WidgetRef ref,
  String method, [
  Json? params,
]) async {
  try {
    return await ref.read(appStateProvider.notifier).invoke(method, params);
  } on RpcError catch (e) {
    if (!context.mounted) return null;
    if (e.code == -32009 && method == 'clients.restore') {
      final choices = await showDialog<Map<String, String>>(
        context: context,
        barrierDismissible: false,
        builder: (_) =>
            ConflictDialog(conflicts: objects(e.data?['conflicts'])),
      );
      if (choices != null && context.mounted) {
        return command(context, ref, method, {...?params, 'choices': choices});
      }
    } else {
      toast(
        context,
        e.code == -32009 ? '${e.message}。請先解除接管並處理衝突後再套用。' : e.message,
      );
    }
  } catch (e) {
    if (context.mounted) toast(context, '操作失敗：$e');
  }
  return null;
}

Future<bool> confirm(
  BuildContext context,
  String title,
  String detail, {
  String action = '確認',
}) async =>
    await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(title),
        content: Text(detail),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: Text(action),
          ),
        ],
      ),
    ) ??
    false;

class ConflictDialog extends StatefulWidget {
  const ConflictDialog({super.key, required this.conflicts});
  final List<Json> conflicts;
  @override
  State<ConflictDialog> createState() => _ConflictDialogState();
}

class _ConflictDialogState extends State<ConflictDialog> {
  final choices = <String, String>{};
  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('設定欄位已被修改'),
    content: SizedBox(
      width: 600,
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('請逐一選擇要保留的值。其他設定將保留，憑證已遮蔽。'),
            for (final conflict in widget.conflicts) ...[
              const SizedBox(height: 20),
              Text(
                '${conflict['field']}',
                style: const TextStyle(fontWeight: FontWeight.bold),
              ),
              const SizedBox(height: 8),
              SelectableText(
                '接管值：${conflict['managed'] ?? '未設定'}\n目前值：${conflict['current'] ?? '未設定'}\n原始值：${conflict['original'] ?? '未設定'}',
              ),
              const SizedBox(height: 8),
              SegmentedButton<String>(
                segments: const [
                  ButtonSegment(value: 'current', label: Text('保留目前值')),
                  ButtonSegment(value: 'original', label: Text('還原原始值')),
                ],
                emptySelectionAllowed: true,
                selected: {
                  if (choices[conflict['field']] case final String v) v,
                },
                onSelectionChanged: (s) => setState(() {
                  if (s.isNotEmpty) choices[conflict['field']] = s.first;
                }),
              ),
            ],
          ],
        ),
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      FilledButton(
        onPressed: choices.length != widget.conflicts.length
            ? null
            : () => Navigator.pop(context, choices),
        child: const Text('套用選擇並還原'),
      ),
    ],
  );
}
