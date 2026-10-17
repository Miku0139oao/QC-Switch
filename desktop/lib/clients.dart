import 'dart:convert';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'brand.dart';
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
  String? profileId, model;
  String mode = 'direct', bindingSignature = '';
  final directory = TextEditingController();
  Json roles = {};
  Json get current =>
      objects(widget.state['clients'])
          .firstWhere((c) => c['client'] == widget.client, orElse: () => {});
  String get nativeProtocol =>
      widget.client == 'claude' ? 'messages' : 'responses';

  @override
  void initState() {
    super.initState();
    syncBinding();
  }

  @override
  void didUpdateWidget(covariant ClientPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    syncBinding();
  }

  void syncBinding() {
    final binding = object(current['binding']);
    final signature = jsonEncode(binding);
    if (signature != bindingSignature) {
      bindingSignature = signature;
      profileId = binding['profileId'];
      model = binding['model'];
      mode = binding['mode'] ?? 'direct';
      directory.text = binding['directory'] ?? '';
      roles = {...object(binding['roles'])};
    }
    // Refreshes preserve an unsaved selection, but invalidate deleted models.
    final profile = objects(widget.state['profiles'])
        .where((p) => p['id'] == profileId)
        .firstOrNull;
    if (profile == null) {
      profileId = null;
      model = null;
      roles = {};
    } else {
      final models = objects(profile['models']);
      if (!models.any((m) => m['id'] == model)) model = null;
      roles.removeWhere((_, value) => !models.any((m) => m['id'] == value));
      if (profile['protocol'] != nativeProtocol) mode = 'route';
    }
  }

  void selectProfile(Json profile) {
    profileId = profile['id'];
    final models = objects(profile['models']);
    model = models.length == 1 ? models.single['id'] : null;
    roles = {};
    mode = profile['protocol'] == nativeProtocol ? 'direct' : 'route';
  }

  Future<void> addModel(Json profile) async {
    final added = await showDialog<Json>(
      context: context,
      builder: (_) => const ModelDialog(),
    );
    if (added == null || !mounted) return;
    final models = objects(profile['models']);
    models.removeWhere((m) => m['id'] == added['id']);
    final result = await command(context, ref, 'profiles.save', {
      ...profile,
      'models': [...models, added],
    });
    if (result != null && mounted) setState(() => model = added['id']);
  }

  @override
  void dispose() {
    directory.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final isClaude = widget.client == 'claude';
    final name = isClaude ? 'Claude Code' : 'Codex';
    final colors = Theme.of(context).colorScheme;
    final profiles = objects(widget.state['profiles']);
    final profile = profiles.where((p) => p['id'] == profileId).firstOrNull;
    final models = objects(profile?['models']);
    final selectedModel = models.where((m) => m['id'] == model).firstOrNull;
    final native = profile?['protocol'] == nativeProtocol;
    final apiSuggestion = quickRouterApiSuggestion(profile?['endpoint']);
    final managed = current['managed'] == true;
    final missing = profile == null
        ? '請先選擇一組 QuickRouter 連線。'
        : apiSuggestion != null
        ? '請先修正 API 位址，再啟用客戶端。'
        : profile['hasKey'] != true
        ? '此連線尚未保存 API Key，請先編輯連線。'
        : models.isEmpty
        ? '此連線尚未加入模型。取得線上清單，或手動新增模型後即可啟用。'
        : selectedModel == null
        ? '請選擇預設模型，即可啟用客戶端。'
        : mode == 'direct' && !native
        ? '此接口需要使用本地路由。'
        : null;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        PageHeading(
          name,
          isClaude ? 'CLI 與 VS Code 擴充套件的連線設定' : 'CLI、VS Code 擴充套件與桌面版的連線設定',
          leading: ClientLogo(widget.client, size: 30, tile: true),
          action: StatusPill(managed ? '已啟用' : '尚未啟用', active: managed),
        ),
        if (profiles.isEmpty)
          EmptyState(
            '先新增連線',
            '加入 QuickRouter Key 與模型，再回到此處啟用。',
            action: FilledButton(
              onPressed: widget.onConnections,
              child: const Text('前往連線設定'),
            ),
          )
        else ...[
          SectionCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const _StepTitle('1', '選擇連線與模型', '每個客戶端可以使用不同的 Key 與模型。'),
                const SizedBox(height: 24),
                DropdownButtonFormField<String>(
                  key: ValueKey(
                    'profile-$profileId-${profiles.map((p) => p['id']).join(',')}',
                  ),
                  initialValue: profile?['id'],
                  isExpanded: true,
                  decoration: const InputDecoration(
                    labelText: 'QuickRouter 連線',
                    hintText: '選擇已保存的連線',
                    prefixIcon: Icon(Icons.key_outlined),
                  ),
                  items: [
                    for (final p in profiles)
                      DropdownMenuItem(
                        value: p['id'] as String,
                        child: Text(
                          '${p['name']} · ${protocolName(p['protocol'])}',
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                  ],
                  onChanged: (v) => setState(
                    () =>
                        selectProfile(profiles.firstWhere((p) => p['id'] == v)),
                  ),
                ),
                if (profile != null) ...[
                  Padding(
                    padding: const EdgeInsets.fromLTRB(4, 10, 4, 18),
                    child: Text(
                      '${profile['endpoint']}',
                      style: TextStyle(
                        fontSize: 12,
                        color: colors.onSurfaceVariant,
                      ),
                    ),
                  ),
                  if (apiSuggestion != null)
                    EndpointHint(
                      endpoint: apiSuggestion,
                      onFix: () => command(context, ref, 'profiles.save', {
                        ...profile,
                        'endpoint': apiSuggestion,
                      }),
                    ),
                ] else
                  const SizedBox(height: 20),
                DropdownButtonFormField<String>(
                  key: ValueKey(
                    'model-$profileId-$model-${models.map((m) => m['id']).join(',')}',
                  ),
                  initialValue: selectedModel?['id'],
                  isExpanded: true,
                  decoration: InputDecoration(
                    labelText: '預設模型',
                    hintText: profile == null
                        ? '請先選擇連線'
                        : models.isEmpty
                        ? '請先加入模型'
                        : '選擇要使用的模型',
                    prefixIcon: const Icon(Icons.auto_awesome_outlined),
                  ),
                  items: [
                    for (final m in models)
                      DropdownMenuItem(
                        value: m['id'] as String,
                        child: Text(
                          '${m['id']}',
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                  ],
                  onChanged: models.isEmpty
                      ? null
                      : (v) => setState(() => model = v),
                ),
                const SizedBox(height: 12),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    if (profile != null) ...[
                      AsyncButton(
                        label: '取得模型',
                        icon: Icons.cloud_download_outlined,
                        onPressed:
                            profile['hasKey'] != true || apiSuggestion != null
                            ? null
                            : () => fetchProfileModels(context, ref, profile),
                      ),
                      TextButton.icon(
                        onPressed: () => addModel(profile),
                        icon: const Icon(Icons.add, size: 18),
                        label: const Text('手動新增模型'),
                      ),
                      TextButton.icon(
                        onPressed: () => showDialog<void>(
                          context: context,
                          builder: (_) => ProfileDialog(profile: profile),
                        ),
                        icon: const Icon(Icons.edit_outlined, size: 18),
                        label: const Text('編輯連線'),
                      ),
                    ] else
                      TextButton.icon(
                        onPressed: widget.onConnections,
                        icon: const Icon(Icons.add, size: 18),
                        label: const Text('管理連線'),
                      ),
                  ],
                ),
                if (selectedModel != null && selectedModel['tools'] != true)
                  const Notice(
                    '此模型尚未標記支援工具呼叫。讀檔、執行命令與修改檔案需要工具能力。',
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
                            key: ValueKey('$profileId-$role-${roles[role]}'),
                            initialValue:
                                models.any((m) => m['id'] == roles[role])
                                ? roles[role] as String
                                : '',
                            isExpanded: true,
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
              ],
            ),
          ),
          const SizedBox(height: 16),
          SectionCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const _StepTitle('2', '選擇接入方式', '套用後的新請求使用此設定，既有串流會繼續完成。'),
                const SizedBox(height: 20),
                SegmentedButton<String>(
                  segments: [
                    ButtonSegment(
                      value: 'direct',
                      label: const Text('直連 · 不開埠'),
                      icon: const Icon(Icons.north_east),
                      enabled: native,
                    ),
                    ButtonSegment(
                      value: 'route',
                      label: const Text('本地路由'),
                      icon: const Icon(Icons.route_outlined),
                      enabled: profile != null,
                    ),
                  ],
                  selected: {mode},
                  onSelectionChanged: (v) => setState(() => mode = v.first),
                ),
                Notice(
                  profile == null
                      ? '選擇連線後，會依接口協議顯示可用的接入方式。'
                      : mode == 'direct'
                      ? '客戶端直接連線 QuickRouter，不啟動監聽。Key 會寫入客戶端設定；此模式不記錄用量。'
                      : '啟用時才監聽 127.0.0.1，提供協議轉換與用量紀錄。客戶端僅保存隨機本機憑證。',
                ),
                if (profile != null && !native)
                  Text(
                    '${protocolName(profile['protocol'])} → ${isClaude ? 'Messages' : 'Responses'}，需要本地路由轉換。',
                    style: TextStyle(color: colors.primary, fontSize: 13),
                  ),
                ExpansionTile(
                  tilePadding: EdgeInsets.zero,
                  title: const Text('進階設定'),
                  subtitle: const Text('設定目錄與客戶端行為'),
                  children: [
                    TextField(
                      controller: directory,
                      decoration: InputDecoration(
                        labelText: '設定目錄覆寫',
                        hintText: '留空使用預設目錄或環境變數',
                        suffixIcon: IconButton(
                          tooltip: '選擇目錄',
                          icon: const Icon(Icons.folder_open_outlined),
                          onPressed: () async {
                            final path = await getDirectoryPath();
                            if (path != null && mounted) directory.text = path;
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
                        color: colors.onSurfaceVariant,
                      ),
                    ),
                    if (!isClaude)
                      const Notice(
                        'Codex 使用獨立 provider，保留原有登入資料。維持 Responses 接口；跨協議模式使用完整歷史重播與本地壓縮，停用遠端搜尋。',
                      ),
                    const SizedBox(height: 12),
                  ],
                ),
              ],
            ),
          ),
          const SizedBox(height: 16),
          SectionCard(
            padding: 20,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (missing != null)
                  Notice(missing, icon: Icons.pending_outlined)
                else
                  const Notice(
                    '設定已就緒。套用後請重新啟動客戶端，載入新的連線設定。',
                    icon: Icons.check_circle_outline,
                  ),
                Wrap(
                  spacing: 12,
                  runSpacing: 12,
                  children: [
                    AsyncButton(
                      label: mode == 'route' ? '啟用本地路由' : '套用直連設定',
                      icon: Icons.check_circle_outline,
                      filled: true,
                      onPressed: missing != null
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
                      onPressed: !managed
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
        ],
        if ((current['warnings'] as List? ?? []).isNotEmpty) ...[
          const SizedBox(height: 16),
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
        ],
      ],
    );
  }
}

class _StepTitle extends StatelessWidget {
  const _StepTitle(this.number, this.title, this.subtitle);
  final String number, title, subtitle;
  @override
  Widget build(BuildContext context) => Row(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      CircleAvatar(
        radius: 15,
        backgroundColor: Theme.of(context).colorScheme.surfaceContainerHighest,
        child: Text(
          number,
          style: TextStyle(
            fontSize: 13,
            color: Theme.of(context).colorScheme.onSurface,
          ),
        ),
      ),
      const SizedBox(width: 12),
      Expanded(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              title,
              style: Theme.of(context).textTheme.titleMedium
                  ?.copyWith(fontWeight: FontWeight.w600),
            ),
            const SizedBox(height: 4),
            Text(
              subtitle,
              style: TextStyle(
                fontSize: 12,
                height: 1.6,
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
            ),
          ],
        ),
      ),
    ],
  );
}
