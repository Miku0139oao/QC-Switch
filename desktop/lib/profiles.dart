import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'core.dart';
import 'widgets.dart';

String protocolName(dynamic value) => switch (value) {
  'messages' => 'Messages',
  'responses' => 'Responses',
  _ => 'Chat Completions',
};

String? quickRouterApiSuggestion(dynamic endpoint) {
  final uri = Uri.tryParse('$endpoint');
  return switch (uri?.host.toLowerCase()) {
    'quickrouter.ai' => 'https://api.quickrouter.ai',
    'quickrouter.us' => 'https://api.quickrouter.us',
    _ => null,
  };
}

class EndpointHint extends StatelessWidget {
  const EndpointHint({super.key, required this.endpoint, required this.onFix});
  final String endpoint;
  final VoidCallback onFix;
  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Notice('目前填入的是網站位址。API 端點應為 $endpoint。', icon: Icons.link_off),
      TextButton.icon(
        onPressed: onFix,
        icon: const Icon(Icons.auto_fix_high, size: 18),
        label: const Text('改用 API 位址'),
      ),
    ],
  );
}

Future<void> fetchProfileModels(
  BuildContext context,
  WidgetRef ref,
  Json profile,
) async {
  final result = await command(context, ref, 'models.fetch', {
    'id': profile['id'],
  });
  if (result != null && context.mounted) {
    await showDialog<void>(
      context: context,
      builder: (_) =>
          DiscoverModelsDialog(profile: profile, models: objects(result)),
    );
  }
}

class ProfilesPage extends ConsumerWidget {
  const ProfilesPage({super.key, required this.state});
  final Json state;
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final profiles = objects(state['profiles']);
    void edit([Json? profile]) => showDialog<void>(
      context: context,
      builder: (_) => ProfileDialog(profile: profile),
    );
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        PageHeading(
          '連線設定',
          '管理 QuickRouter 的節點、Key 與模型能力。每個客戶端可使用不同連線。',
          action: FilledButton.icon(
            onPressed: edit,
            icon: const Icon(Icons.add),
            label: const Text('新增連線'),
          ),
        ),
        if (profiles.isEmpty)
          EmptyState(
            '建立第一組連線',
            '新增 API Key，選擇節點與接口協議，再加入要使用的模型。\nKey 只保存在這台電腦的加密資料庫。',
            action: FilledButton(onPressed: edit, child: const Text('新增連線')),
          ),
        for (final p in profiles)
          Padding(
            padding: const EdgeInsets.only(bottom: 16),
            child: SectionCard(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Container(
                        padding: const EdgeInsets.all(12),
                        decoration: BoxDecoration(
                          color: Theme.of(context).colorScheme.primaryContainer,
                          borderRadius: BorderRadius.circular(14),
                        ),
                        child: const Icon(Icons.key_outlined),
                      ),
                      const SizedBox(width: 16),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              '${p['name']}',
                              style: Theme.of(context).textTheme.titleLarge,
                            ),
                            const SizedBox(height: 6),
                            Text(
                              '${p['endpoint']}',
                              style: TextStyle(
                                color: Theme.of(context)
                                    .colorScheme
                                    .onSurfaceVariant,
                              ),
                            ),
                          ],
                        ),
                      ),
                      StatusPill(
                        p['hasKey'] == true ? 'Key 已加密' : '尚未設定 Key',
                        active: p['hasKey'] == true,
                      ),
                      const SizedBox(width: 8),
                      IconButton(
                        tooltip: '編輯連線',
                        onPressed: () => edit(p),
                        icon: const Icon(Icons.edit_outlined),
                      ),
                      IconButton(
                        tooltip: '刪除連線',
                        onPressed: () async {
                          if (await confirm(
                                context,
                                '刪除此連線？',
                                '已啟用此連線的客戶端必須先解除接管。',
                                action: '刪除',
                              ) &&
                              context.mounted) {
                            await command(context, ref, 'profiles.delete', {
                              'id': p['id'],
                            });
                          }
                        },
                        icon: const Icon(Icons.delete_outline),
                      ),
                    ],
                  ),
                  const SizedBox(height: 20),
                  if (quickRouterApiSuggestion(p['endpoint'])
                      case final String api)
                    EndpointHint(
                      endpoint: api,
                      onFix: () => command(context, ref, 'profiles.save', {
                        ...p,
                        'endpoint': api,
                      }),
                    ),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      Chip(
                        label: Text(protocolName(p['protocol'])),
                        avatar: const Icon(Icons.api, size: 16),
                      ),
                      for (final m in objects(p['models']))
                        Chip(
                          label: Text('${m['id']}'),
                          avatar: Icon(
                            m['tools'] == true
                                ? Icons.build_outlined
                                : Icons.smart_toy_outlined,
                            size: 16,
                          ),
                        ),
                    ],
                  ),
                  if (objects(p['models']).isEmpty)
                    const Notice('尚未加入模型。可手動輸入，或從此 Key 的模型清單選擇。'),
                  const SizedBox(height: 16),
                  Wrap(
                    spacing: 12,
                    runSpacing: 12,
                    children: [
                      AsyncButton(
                        label: '取得模型',
                        icon: Icons.cloud_download_outlined,
                        onPressed: p['hasKey'] != true
                            ? null
                            : () => fetchProfileModels(context, ref, p),
                      ),
                      OutlinedButton.icon(
                        onPressed:
                            p['hasKey'] != true || objects(p['models']).isEmpty
                            ? null
                            : () => showDialog<void>(
                                context: context,
                                builder: (_) => ProbeDialog(profile: p),
                              ),
                        icon: const Icon(Icons.network_check),
                        label: const Text('連線測試'),
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ),
      ],
    );
  }
}

class ProfileDialog extends ConsumerStatefulWidget {
  const ProfileDialog({super.key, this.profile});
  final Json? profile;
  @override
  ConsumerState<ProfileDialog> createState() => _ProfileDialogState();
}

class _ProfileDialogState extends ConsumerState<ProfileDialog> {
  final form = GlobalKey<FormState>();
  late final name = TextEditingController(text: widget.profile?['name']);
  late final endpoint = TextEditingController(
    text: widget.profile?['endpoint'] ?? 'https://api.quickrouter.ai',
  );
  final keyField = TextEditingController();
  late String protocol = widget.profile?['protocol'] ?? 'responses';
  late List<Json> models = objects(widget.profile?['models']);
  bool reveal = false;
  @override
  void dispose() {
    name.dispose();
    endpoint.dispose();
    keyField.dispose();
    super.dispose();
  }

  Future<void> editModel([int? i]) async {
    final result = await showDialog<Json>(
      context: context,
      builder: (_) => ModelDialog(model: i == null ? null : models[i]),
    );
    if (result != null) {
      setState(() {
        if (i == null) {
          models.add(result);
        } else {
          models[i] = result;
        }
      });
    }
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: Text(widget.profile == null ? '新增 QuickRouter 連線' : '編輯連線'),
    content: SizedBox(
      width: 660,
      child: SingleChildScrollView(
        child: Form(
          key: form,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              TextFormField(
                controller: name,
                decoration: const InputDecoration(
                  labelText: '連線名稱',
                  hintText: '例如：主要開發 Key',
                ),
                validator: (v) => v!.trim().isEmpty ? '請輸入名稱' : null,
              ),
              const SizedBox(height: 20),
              Wrap(
                spacing: 8,
                children: [
                  for (final node in ['us', 'ai'])
                    ActionChip(
                      label: Text('api.quickrouter.$node'),
                      onPressed: () => setState(
                        () => endpoint.text = 'https://api.quickrouter.$node',
                      ),
                    ),
                ],
              ),
              const SizedBox(height: 12),
              TextFormField(
                controller: endpoint,
                decoration: const InputDecoration(
                  labelText: '節點或自訂端點',
                  helperText: '接受根地址或 /v1；不會自動切換節點',
                ),
                onChanged: (_) => setState(() {}),
                validator: (v) => Uri.tryParse(v!.trim())?.hasAuthority == true
                    ? null
                    : '請輸入完整網址',
              ),
              if (quickRouterApiSuggestion(endpoint.text) case final String api)
                EndpointHint(
                  endpoint: api,
                  onFix: () => setState(() => endpoint.text = api),
                ),
              const SizedBox(height: 20),
              DropdownButtonFormField<String>(
                initialValue: protocol,
                decoration: const InputDecoration(labelText: '上游接口協議'),
                items: [
                  for (final p in ['responses', 'messages', 'chat'])
                    DropdownMenuItem(value: p, child: Text(protocolName(p))),
                ],
                onChanged: (v) => setState(() => protocol = v!),
              ),
              const SizedBox(height: 20),
              TextFormField(
                controller: keyField,
                obscureText: !reveal,
                autocorrect: false,
                enableSuggestions: false,
                decoration: InputDecoration(
                  labelText: 'API Key',
                  helperText: widget.profile?['hasKey'] == true
                      ? '留空保留原 Key；輸入內容會取代原 Key'
                      : '使用 Windows DPAPI 加密保存',
                  suffixIcon: IconButton(
                    tooltip: reveal ? '隱藏' : '顯示',
                    onPressed: () => setState(() => reveal = !reveal),
                    icon: Icon(
                      reveal
                          ? Icons.visibility_off_outlined
                          : Icons.visibility_outlined,
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 24),
              Row(
                children: [
                  Expanded(
                    child: Text(
                      '模型與能力',
                      style: Theme.of(context).textTheme.titleMedium,
                    ),
                  ),
                  TextButton.icon(
                    onPressed: editModel,
                    icon: const Icon(Icons.add),
                    label: const Text('手動新增'),
                  ),
                ],
              ),
              const Text(
                '依實際模型文件設定能力；清單可見不代表此 Key 具有使用權限。',
                style: TextStyle(fontSize: 12, height: 1.6),
              ),
              for (var i = 0; i < models.length; i++)
                ListTile(
                  contentPadding: EdgeInsets.zero,
                  title: Text('${models[i]['id']}'),
                  subtitle: Text(
                    '${models[i]['tools'] == true ? '工具 · ' : ''}${models[i]['vision'] == true ? '圖片 · ' : ''}${models[i]['context'] == 0 ? '上下文未指定' : '${models[i]['context']} tokens'}',
                  ),
                  onTap: () => editModel(i),
                  trailing: IconButton(
                    tooltip: '移除模型',
                    onPressed: () => setState(() => models.removeAt(i)),
                    icon: const Icon(Icons.remove_circle_outline),
                  ),
                ),
            ],
          ),
        ),
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      AsyncButton(
        label: '保存連線',
        filled: true,
        icon: Icons.save_outlined,
        onPressed: () async {
          if (!form.currentState!.validate()) return;
          final result = await command(context, ref, 'profiles.save', {
            'id': widget.profile?['id'] ?? '',
            'name': name.text.trim(),
            'endpoint': endpoint.text.trim(),
            'protocol': protocol,
            'key': keyField.text.trim(),
            'models': models,
          });
          if (result != null && context.mounted) Navigator.pop(context);
        },
      ),
    ],
  );
}

class ModelDialog extends StatefulWidget {
  const ModelDialog({super.key, this.model});
  final Json? model;
  @override
  State<ModelDialog> createState() => _ModelDialogState();
}

class _ModelDialogState extends State<ModelDialog> {
  final form = GlobalKey<FormState>();
  late final id = TextEditingController(text: widget.model?['id']);
  late final contextSize = TextEditingController(
    text: '${widget.model?['context'] ?? 0}',
  );
  late final efforts = TextEditingController(
    text: (widget.model?['efforts'] as List? ?? ['low', 'medium', 'high']).join(
      ',',
    ),
  );
  late bool tools = widget.model?['tools'] == true,
      vision = widget.model?['vision'] == true,
      reasoning = widget.model?['reasoning'] == true;
  @override
  void dispose() {
    id.dispose();
    contextSize.dispose();
    efforts.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('模型能力'),
    content: SizedBox(
      width: 440,
      child: Form(
        key: form,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextFormField(
              controller: id,
              decoration: const InputDecoration(labelText: '模型 ID'),
              validator: (v) => v!.trim().isEmpty ? '請輸入模型 ID' : null,
            ),
            const SizedBox(height: 20),
            TextFormField(
              controller: contextSize,
              decoration: const InputDecoration(
                labelText: '上下文 tokens',
                helperText: '0 代表未知；Codex 採保守 32,000',
              ),
              keyboardType: TextInputType.number,
              validator: (v) => (int.tryParse(v!) ?? -1) < 0 ? '請輸入非負整數' : null,
            ),
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: const Text('工具呼叫'),
              value: tools,
              onChanged: (v) => setState(() => tools = v),
            ),
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: const Text('圖片輸入'),
              value: vision,
              onChanged: (v) => setState(() => vision = v),
            ),
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: const Text('推理參數'),
              value: reasoning,
              onChanged: (v) => setState(() => reasoning = v),
            ),
            if (reasoning)
              TextFormField(
                controller: efforts,
                decoration: const InputDecoration(
                  labelText: '推理強度（逗號分隔）',
                  helperText: '例如 low,medium,high',
                ),
              ),
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
        onPressed: () {
          if (!form.currentState!.validate()) return;
          Navigator.pop(context, <String, dynamic>{
            'id': id.text.trim(),
            'name': id.text.trim(),
            'context': int.parse(contextSize.text),
            'tools': tools,
            'vision': vision,
            'reasoning': reasoning,
            'efforts': reasoning
                ? efforts.text
                      .split(',')
                      .map((e) => e.trim())
                      .where((e) => e.isNotEmpty)
                      .toList()
                : <String>[],
          });
        },
        child: const Text('保存模型'),
      ),
    ],
  );
}

class DiscoverModelsDialog extends ConsumerStatefulWidget {
  const DiscoverModelsDialog({
    super.key,
    required this.profile,
    required this.models,
  });
  final Json profile;
  final List<Json> models;
  @override
  ConsumerState<DiscoverModelsDialog> createState() =>
      _DiscoverModelsDialogState();
}

class _DiscoverModelsDialogState extends ConsumerState<DiscoverModelsDialog> {
  final selected = <String>{};
  String query = '';
  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('從線上清單加入模型'),
    content: SizedBox(
      width: 580,
      height: 460,
      child: Column(
        children: [
          const Notice('此清單僅供選擇模型 ID。加入後請編輯工具、圖片與上下文能力。'),
          TextField(
            decoration: const InputDecoration(
              prefixIcon: Icon(Icons.search),
              labelText: '搜尋模型',
            ),
            onChanged: (v) => setState(() => query = v.toLowerCase()),
          ),
          const SizedBox(height: 12),
          Expanded(
            child: ListView(
              children: [
                for (final m in widget.models.where(
                  (m) => '${m['id']}'.toLowerCase().contains(query),
                ))
                  CheckboxListTile(
                    title: Text('${m['id']}'),
                    value: selected.contains(m['id']),
                    onChanged: (v) => setState(() {
                      if (v == true) {
                        selected.add(m['id']);
                      } else {
                        selected.remove(m['id']);
                      }
                    }),
                  ),
              ],
            ),
          ),
        ],
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      AsyncButton(
        label: '加入 ${selected.length} 個模型',
        filled: true,
        icon: Icons.add,
        onPressed: selected.isEmpty
            ? null
            : () async {
                final models = objects(widget.profile['models']);
                for (final id in selected) {
                  if (!models.any((m) => m['id'] == id)) {
                    models.add({
                      'id': id,
                      'name': id,
                      'tools': false,
                      'vision': false,
                      'reasoning': false,
                      'context': 0,
                    });
                  }
                }
                final result = await command(context, ref, 'profiles.save', {
                  ...widget.profile,
                  'models': models,
                });
                if (result != null && context.mounted) Navigator.pop(context);
              },
      ),
    ],
  );
}

class ProbeDialog extends ConsumerStatefulWidget {
  const ProbeDialog({super.key, required this.profile});
  final Json profile;
  @override
  ConsumerState<ProbeDialog> createState() => _ProbeDialogState();
}

class _ProbeDialogState extends ConsumerState<ProbeDialog> {
  late String model = objects(widget.profile['models']).first['id'];
  final results = <String, Json>{};
  bool busy = false;
  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('QuickRouter 連線測試'),
    content: SizedBox(
      width: 620,
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Notice(
              '按下測試會向 QuickRouter 發送 API 請求，可能產生費用。工具測試只要求無害的示例呼叫，不執行本機命令。',
              icon: Icons.payments_outlined,
            ),
            DropdownButtonFormField<String>(
              initialValue: model,
              decoration: const InputDecoration(labelText: '測試模型'),
              items: [
                for (final m in objects(widget.profile['models']))
                  DropdownMenuItem(
                    value: m['id'] as String,
                    child: Text('${m['id']}'),
                  ),
              ],
              onChanged: busy
                  ? null
                  : (v) => setState(() {
                      model = v!;
                      results.clear();
                    }),
            ),
            const SizedBox(height: 16),
            for (final entry in {
              'auth': '認證',
              'text': '文字',
              'stream': '串流',
              'tools': '工具呼叫',
            }.entries)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 8),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    SizedBox(
                      width: 104,
                      child: OutlinedButton(
                        onPressed: busy
                            ? null
                            : () async {
                                setState(() => busy = true);
                                final result = await command(
                                  context,
                                  ref,
                                  'connection.test',
                                  {
                                    'id': widget.profile['id'],
                                    'model': model,
                                    'kind': entry.key,
                                  },
                                );
                                if (mounted) {
                                  setState(() {
                                    busy = false;
                                    if (result != null) {
                                      results[entry.key] = object(result);
                                    }
                                  });
                                }
                              },
                        child: Text(entry.value),
                      ),
                    ),
                    const SizedBox(width: 16),
                    Expanded(
                      child: Padding(
                        padding: const EdgeInsets.only(top: 10),
                        child: Text(
                          results[entry.key]?['message'] ?? '尚未測試',
                          style: TextStyle(
                            height: 1.6,
                            color: results[entry.key]?['ok'] == false
                                ? Theme.of(context).colorScheme.error
                                : null,
                          ),
                        ),
                      ),
                    ),
                    if (results[entry.key] != null)
                      Padding(
                        padding: const EdgeInsets.only(top: 12),
                        child: Icon(
                          results[entry.key]!['ok'] == true
                              ? Icons.check_circle_outline
                              : Icons.error_outline,
                          size: 20,
                          color: results[entry.key]!['ok'] == true
                              ? Theme.of(context).colorScheme.primary
                              : Theme.of(context).colorScheme.error,
                        ),
                      ),
                  ],
                ),
              ),
            if (busy) const LinearProgressIndicator(),
          ],
        ),
      ),
    ),
    actions: [
      TextButton(
        onPressed: busy ? null : () => Navigator.pop(context),
        child: const Text('完成'),
      ),
    ],
  );
}
