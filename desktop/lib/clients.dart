import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'core.dart';
import 'profiles.dart';
import 'widgets.dart';

class ClientPage extends ConsumerStatefulWidget {
  const ClientPage({
    super.key,
    required this.client,
    required this.state,
    required this.onConnections,
  });
  final String client;
  final Json state;
  final VoidCallback onConnections;
  @override
  ConsumerState<ClientPage> createState() => _ClientPageState();
}

class _ClientPageState extends ConsumerState<ClientPage> {
  late Json current = objects(widget.state['clients'])
      .firstWhere((c) => c['client'] == widget.client, orElse: () => {});
  late Json binding = object(current['binding']);
  late String? profileId = binding['profileId'];
  late String? model = binding['model'];
  late String mode = binding['mode'] ?? 'direct';
  late final directory = TextEditingController(text: binding['directory']);
  late Json roles = object(binding['roles']);
  @override
  void dispose() {
    directory.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    current = objects(widget.state['clients'])
        .firstWhere((c) => c['client'] == widget.client, orElse: () => {});
    final isClaude = widget.client == 'claude';
    final name = isClaude ? 'Claude Code' : 'Codex';
    final profiles = objects(widget.state['profiles']);
    final profile = profiles.where((p) => p['id'] == profileId).firstOrNull;
    final models = objects(profile?['models']);
    final selectedModel = models.where((m) => m['id'] == model).firstOrNull;
    final native =
        profile?['protocol'] == (isClaude ? 'messages' : 'responses');
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        PageHeading(
          name,
          isClaude
              ? '管理 CLI 與 VS Code 擴充套件共用的使用者設定。'
              : '管理 CLI、VS Code 擴充套件與 Windows 桌面版的共用設定。',
          action: StatusPill(
            current['managed'] == true ? '已接管設定' : '尚未接管',
            active: current['managed'] == true,
          ),
        ),
        if (profiles.isEmpty)
          EmptyState(
            '先新增連線',
            '客戶端需要一組 Key 與至少一個模型。',
            action: FilledButton(
              onPressed: widget.onConnections,
              child: const Text('前往連線設定'),
            ),
          )
        else
          SectionCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('連線與模型', style: Theme.of(context).textTheme.titleLarge),
                const SizedBox(height: 24),
                DropdownButtonFormField<String>(
                  key: ValueKey('profile-$profileId-${profiles.length}'),
                  initialValue: profile?['id'],
                  decoration: const InputDecoration(
                    labelText: 'QuickRouter 連線',
                  ),
                  items: [
                    for (final p in profiles)
                      DropdownMenuItem(
                        value: p['id'] as String,
                        child: Text(
                          '${p['name']} · ${protocolName(p['protocol'])}',
                        ),
                      ),
                  ],
                  onChanged: (v) => setState(() {
                    profileId = v;
                    model = null;
                    roles = {};
                    if (profiles.firstWhere((p) => p['id'] == v)['protocol'] !=
                        (isClaude ? 'messages' : 'responses')) {
                      mode = 'route';
                    }
                  }),
                ),
                const SizedBox(height: 24),
                DropdownButtonFormField<String>(
                  key: ValueKey('model-$profileId-$model-${models.length}'),
                  initialValue: selectedModel?['id'],
                  decoration: const InputDecoration(
                    labelText: '預設模型',
                    helperText: '新請求使用此模型；切換連線不會中斷既有串流',
                  ),
                  items: [
                    for (final m in models)
                      DropdownMenuItem(
                        value: m['id'] as String,
                        child: Text('${m['id']}'),
                      ),
                  ],
                  onChanged: (v) => setState(() => model = v),
                ),
                const SizedBox(height: 24),
                Text('接入模式', style: Theme.of(context).textTheme.titleMedium),
                const SizedBox(height: 12),
                SegmentedButton<String>(
                  segments: [
                    ButtonSegment(
                      value: 'direct',
                      label: const Text('直連 · 不開埠'),
                      icon: const Icon(Icons.north_east),
                      enabled: native,
                    ),
                    const ButtonSegment(
                      value: 'route',
                      label: Text('本地路由'),
                      icon: Icon(Icons.route_outlined),
                    ),
                  ],
                  selected: {mode},
                  onSelectionChanged: (v) => setState(() => mode = v.first),
                ),
                Notice(
                  mode == 'direct'
                      ? '客戶端直接連線 QuickRouter，Key 會寫入該客戶端的本機設定檔。此模式不經過 QC Switch，無法統計直連流量。退出時仍會還原接管的設定。'
                      : '啟用後才監聽 127.0.0.1。客戶端只保存隨機本機憑證；真正的 Key 由核心注入。本地路由支援協議轉換與用量紀錄。',
                ),
                if (!native && profile != null)
                  Notice(
                    '${protocolName(profile['protocol'])} 需要透過本地路由轉換成 ${isClaude ? 'Messages' : 'Responses'}。',
                  ),
                if (selectedModel != null && selectedModel['tools'] != true)
                  const Notice(
                    '此模型尚未標記支援工具呼叫。讀檔、執行命令與修改檔案需要模型具備工具能力。',
                    icon: Icons.build_outlined,
                  ),
                if (isClaude && models.isNotEmpty)
                  ExpansionTile(
                    tilePadding: EdgeInsets.zero,
                    title: const Text('角色模型映射'),
                    subtitle: const Text('未指定時沿用預設模型'),
                    children: [
                      for (final role in [
                        'sonnet',
                        'opus',
                        'haiku',
                        'subagent',
                      ])
                        Padding(
                          padding: const EdgeInsets.only(bottom: 16),
                          child: DropdownButtonFormField<String>(
                            key: ValueKey('$profileId-$role'),
                            initialValue:
                                models.any((m) => m['id'] == roles[role])
                                ? roles[role] as String
                                : '',
                            decoration: InputDecoration(labelText: role),
                            items: [
                              const DropdownMenuItem(
                                value: '',
                                child: Text('沿用預設模型'),
                              ),
                              for (final m in models)
                                DropdownMenuItem(
                                  value: m['id'] as String,
                                  child: Text('${m['id']}'),
                                ),
                            ],
                            onChanged: (v) => setState(() => roles[role] = v),
                          ),
                        ),
                    ],
                  ),
                const SizedBox(height: 20),
                TextField(
                  controller: directory,
                  decoration: InputDecoration(
                    labelText: '設定目錄覆寫',
                    hintText: '留空使用客戶端預設目錄或環境變數',
                    suffixIcon: IconButton(
                      tooltip: '選擇目錄',
                      icon: const Icon(Icons.folder_open_outlined),
                      onPressed: () async {
                        final path = await getDirectoryPath();
                        if (path != null) directory.text = path;
                      },
                    ),
                  ),
                ),
                const SizedBox(height: 12),
                SelectableText(
                  '目前設定位置：${current['path'] ?? ''}',
                  style: TextStyle(
                    fontSize: 12,
                    height: 1.6,
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                ),
                const SizedBox(height: 20),
                Wrap(
                  spacing: 12,
                  runSpacing: 12,
                  children: [
                    AsyncButton(
                      label: mode == 'route' ? '啟用本地路由' : '套用直連設定',
                      icon: Icons.check_circle_outline,
                      filled: true,
                      onPressed:
                          profile == null ||
                              selectedModel == null ||
                              (mode == 'direct' && !native)
                          ? null
                          : () async {
                              final result = await command(
                                context,
                                ref,
                                'clients.apply',
                                {
                                  'client': widget.client,
                                  'profileId': profileId,
                                  'model': model,
                                  'mode': mode,
                                  'directory': directory.text.trim(),
                                  'roles': roles,
                                },
                              );
                              if (result != null && context.mounted) {
                                toast(context, '$name 設定已套用。請重新啟動該客戶端以載入新設定。');
                              }
                            },
                    ),
                    AsyncButton(
                      label: '解除接管並還原',
                      icon: Icons.restore,
                      onPressed: current['managed'] != true
                          ? null
                          : () async {
                              final result = await command(
                                context,
                                ref,
                                'clients.restore',
                                {'client': widget.client},
                              );
                              if (result != null && context.mounted) {
                                toast(context, '已還原 $name 設定。請重新啟動客戶端。');
                              }
                            },
                    ),
                  ],
                ),
              ],
            ),
          ),
        const SizedBox(height: 20),
        if ((current['warnings'] as List? ?? []).isNotEmpty)
          SectionCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('設定診斷', style: Theme.of(context).textTheme.titleMedium),
                for (final warning in current['warnings'] as List)
                  Notice('$warning', icon: Icons.manage_search),
              ],
            ),
          ),
        if (!isClaude)
          const Notice(
            'Codex 使用獨立 provider，保留原有登入資料；維持 Responses 接口，停用遠端搜尋。跨協議模式使用完整歷史重播與本地壓縮。',
          ),
      ],
    );
  }
}
