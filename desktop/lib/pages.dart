import 'dart:convert';
import 'dart:io';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'core.dart';
import 'widgets.dart';

class OverviewPage extends StatelessWidget {
  const OverviewPage({super.key, required this.state, required this.navigate});
  final Json state;
  final ValueChanged<int> navigate;
  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).colorScheme;
    final profiles = objects(state['profiles']);
    final clients = objects(state['clients']);
    final records = objects(state['records']);
    final errors = records
        .where((r) => (r['status'] as num? ?? 0) >= 400)
        .toList();
    final running = state['running'] == true;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const PageHeading('總覽', '讓開發工具連上你的 QuickRouter，設定與流量盡在掌握。'),
        Container(
          padding: const EdgeInsets.all(28),
          decoration: BoxDecoration(
            color: c.primaryContainer,
            borderRadius: BorderRadius.circular(24),
          ),
          child: Row(
            children: [
              Container(
                padding: const EdgeInsets.all(18),
                decoration: BoxDecoration(
                  color: c.primary.withValues(alpha: .1),
                  shape: BoxShape.circle,
                ),
                child: Icon(
                  running ? Icons.route : Icons.power_settings_new,
                  size: 32,
                  color: c.onPrimaryContainer,
                ),
              ),
              const SizedBox(width: 24),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      running ? '本地路由運作中' : '準備好，再啟用路由',
                      style: Theme.of(context).textTheme.headlineSmall
                          ?.copyWith(
                            color: c.onPrimaryContainer,
                            fontWeight: FontWeight.w600,
                          ),
                    ),
                    const SizedBox(height: 10),
                    Text(
                      running
                          ? '${state['address']} · 新請求使用目前連線設定'
                          : '管理設定不占用埠號。啟用本地路由時才開始監聽。',
                      style: TextStyle(
                        color: c.onPrimaryContainer,
                        height: 1.6,
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 20),
              StatusPill(running ? '運作中' : '未監聽', active: running),
            ],
          ),
        ),
        const SizedBox(height: 20),
        Row(
          children: [
            Expanded(
              child: _Metric('已保存連線', '${profiles.length}', Icons.key_outlined),
            ),
            const SizedBox(width: 16),
            Expanded(
              child: _Metric('活動請求', '${state['active'] ?? 0}', Icons.sync),
            ),
            const SizedBox(width: 16),
            Expanded(
              child: _Metric('近期錯誤', '${errors.length}', Icons.error_outline),
            ),
          ],
        ),
        const SizedBox(height: 28),
        Text('客戶端', style: Theme.of(context).textTheme.titleLarge),
        const SizedBox(height: 16),
        for (final client in clients)
          Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: SectionCard(
              padding: 20,
              child: Row(
                children: [
                  Icon(
                    client['client'] == 'claude' ? Icons.terminal : Icons.code,
                    size: 28,
                    color: c.primary,
                  ),
                  const SizedBox(width: 20),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          client['client'] == 'claude'
                              ? 'Claude Code'
                              : 'Codex',
                          style: Theme.of(context).textTheme.titleMedium,
                        ),
                        const SizedBox(height: 6),
                        Text(
                          client['managed'] == true
                              ? '${object(client['binding'])['model']} · ${object(client['binding'])['mode'] == 'route' ? '本地路由' : '直連'}'
                              : '尚未啟用 · 選擇連線與模型',
                          style: TextStyle(color: c.onSurfaceVariant),
                        ),
                      ],
                    ),
                  ),
                  StatusPill(
                    client['managed'] == true ? '已接管' : '未接管',
                    active: client['managed'] == true,
                  ),
                  const SizedBox(width: 12),
                  IconButton(
                    tooltip: '管理客戶端',
                    onPressed: () =>
                        navigate(client['client'] == 'claude' ? 2 : 3),
                    icon: const Icon(Icons.arrow_forward),
                  ),
                ],
              ),
            ),
          ),
        const SizedBox(height: 20),
        if (profiles.isEmpty)
          SectionCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('三步開始', style: Theme.of(context).textTheme.titleLarge),
                const SizedBox(height: 16),
                const Notice(
                  '01  新增 Key 與 QuickRouter 節點',
                  icon: Icons.key_outlined,
                ),
                const Notice('02  選擇模型並測試連線', icon: Icons.network_check),
                const Notice(
                  '03  啟用 Claude Code 或 Codex',
                  icon: Icons.check_circle_outline,
                ),
                const SizedBox(height: 16),
                FilledButton.icon(
                  onPressed: () => navigate(1),
                  icon: const Icon(Icons.add),
                  label: const Text('建立第一組連線'),
                ),
              ],
            ),
          ),
        if (errors.isNotEmpty)
          SectionCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('近期錯誤', style: Theme.of(context).textTheme.titleLarge),
                const SizedBox(height: 12),
                for (final r in errors.take(3))
                  ListTile(
                    contentPadding: EdgeInsets.zero,
                    leading: Icon(Icons.error_outline, color: c.error),
                    title: Text('${r['model']} · HTTP ${r['status']}'),
                    subtitle: Text('${r['error'] ?? '請求失敗'}'),
                  ),
                TextButton(
                  onPressed: () => navigate(4),
                  child: const Text('查看用量紀錄'),
                ),
              ],
            ),
          ),
        for (final message in state['recovery'] as List? ?? [])
          Notice('$message', icon: Icons.restore),
      ],
    );
  }
}

class _Metric extends StatelessWidget {
  const _Metric(this.label, this.value, this.icon);
  final String label, value;
  final IconData icon;
  @override
  Widget build(BuildContext context) => SectionCard(
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Icon(icon, size: 20, color: Theme.of(context).colorScheme.primary),
            const SizedBox(width: 10),
            Text(
              label,
              style: TextStyle(
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
            ),
          ],
        ),
        const SizedBox(height: 20),
        Text(
          value,
          style: Theme.of(context).textTheme.headlineMedium
              ?.copyWith(fontWeight: FontWeight.w600),
        ),
      ],
    ),
  );
}

class UsagePage extends ConsumerStatefulWidget {
  const UsagePage({super.key, required this.state});
  final Json state;
  @override
  ConsumerState<UsagePage> createState() => _UsagePageState();
}

class _UsagePageState extends ConsumerState<UsagePage> {
  String client = 'all', query = '';
  bool errorsOnly = false;
  @override
  Widget build(BuildContext context) {
    final records = objects(widget.state['records'])
        .where(
          (r) =>
              (client == 'all' || r['client'] == client) &&
              (!errorsOnly || (r['status'] as num? ?? 0) >= 400) &&
              '${r['model']} ${r['profile']}'.toLowerCase().contains(
                query.toLowerCase(),
              ),
        )
        .toList();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        PageHeading(
          '用量紀錄',
          '本機路由最近 100 筆請求。僅採用上游回報的 token，不保存提示詞與程式碼。',
          action: AsyncButton(
            label: '清除紀錄',
            icon: Icons.delete_outline,
            onPressed: () async {
              if (await confirm(
                    context,
                    '清除本機紀錄？',
                    '這只會刪除本機請求紀錄，不影響 QuickRouter 帳戶帳單。',
                    action: '清除',
                  ) &&
                  context.mounted) {
                await command(context, ref, 'records.clear');
              }
            },
          ),
        ),
        Row(
          children: [
            Expanded(
              child: TextField(
                decoration: const InputDecoration(
                  labelText: '搜尋模型或連線',
                  prefixIcon: Icon(Icons.search),
                ),
                onChanged: (v) => setState(() => query = v),
              ),
            ),
            const SizedBox(width: 16),
            DropdownButton<String>(
              value: client,
              items: const [
                DropdownMenuItem(value: 'all', child: Text('所有客戶端')),
                DropdownMenuItem(value: 'claude', child: Text('Claude Code')),
                DropdownMenuItem(value: 'codex', child: Text('Codex')),
              ],
              onChanged: (v) => setState(() => client = v!),
            ),
            const SizedBox(width: 16),
            FilterChip(
              label: const Text('僅顯示錯誤'),
              selected: errorsOnly,
              onSelected: (v) => setState(() => errorsOnly = v),
            ),
          ],
        ),
        const SizedBox(height: 20),
        if (records.isEmpty)
          const EmptyState(
            '尚無符合的請求紀錄',
            '透過本地路由發出請求後，會在此顯示狀態、延遲及用量。\n直連模式的流量不經過 QC Switch。',
            icon: Icons.receipt_long_outlined,
          )
        else
          SectionCard(
            padding: 12,
            child: SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              child: DataTable(
                columns: const [
                  DataColumn(label: Text('時間 / 客戶端')),
                  DataColumn(label: Text('模型 / 連線')),
                  DataColumn(label: Text('狀態')),
                  DataColumn(label: Text('延遲')),
                  DataColumn(label: Text('輸入 / 輸出 token')),
                ],
                rows: [
                  for (final r in records)
                    DataRow(
                      cells: [
                        DataCell(
                          Column(
                            mainAxisAlignment: MainAxisAlignment.center,
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(formatTime(r['time'])),
                              Text(
                                '${r['client']}',
                                style: const TextStyle(fontSize: 11),
                              ),
                            ],
                          ),
                        ),
                        DataCell(
                          Tooltip(
                            message: '${r['error'] ?? r['profile']}',
                            child: Column(
                              mainAxisAlignment: MainAxisAlignment.center,
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text('${r['model']}'),
                                Text(
                                  '${r['profile']}',
                                  style: const TextStyle(fontSize: 11),
                                ),
                              ],
                            ),
                          ),
                        ),
                        DataCell(
                          Text(
                            '${r['status']}',
                            style: TextStyle(
                              color: (r['status'] as num? ?? 0) >= 400
                                  ? Theme.of(context).colorScheme.error
                                  : Theme.of(context).colorScheme.primary,
                            ),
                          ),
                        ),
                        DataCell(Text('${r['durationMs']} ms')),
                        DataCell(
                          Text(
                            r['usageKnown'] == true
                                ? '${r['inputTokens']} / ${r['outputTokens']}'
                                : '上游未回報',
                          ),
                        ),
                      ],
                    ),
                ],
              ),
            ),
          ),
        const Notice(
          '這是本機觀測紀錄，並非帳戶結算。餘額與費用請以 QuickRouter 官方頁面為準。',
          icon: Icons.payments_outlined,
        ),
      ],
    );
  }
}

String formatTime(dynamic value) =>
    DateTime.tryParse('$value')?.toLocal().toString().split('.').first ??
    '$value';

class SettingsPage extends ConsumerStatefulWidget {
  const SettingsPage({super.key, required this.state});
  final Json state;
  @override
  ConsumerState<SettingsPage> createState() => _SettingsPageState();
}

class _SettingsPageState extends ConsumerState<SettingsPage> {
  final form = GlobalKey<FormState>();
  late String theme = widget.state['settings']['theme'];
  late final port = TextEditingController(
    text: '${widget.state['settings']['port']}',
  );
  late final retention = TextEditingController(
    text: '${widget.state['settings']['retentionDays']}',
  );
  @override
  void dispose() {
    port.dispose();
    retention.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      const PageHeading('設定', '個人化介面、路由偏好與 portable 資料。'),
      SectionCard(
        child: Form(
          key: form,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('外觀與路由', style: Theme.of(context).textTheme.titleLarge),
              const SizedBox(height: 20),
              SegmentedButton<String>(
                segments: const [
                  ButtonSegment(
                    value: 'system',
                    label: Text('跟隨系統'),
                    icon: Icon(Icons.brightness_auto_outlined),
                  ),
                  ButtonSegment(
                    value: 'light',
                    label: Text('淺色'),
                    icon: Icon(Icons.light_mode_outlined),
                  ),
                  ButtonSegment(
                    value: 'dark',
                    label: Text('深色'),
                    icon: Icon(Icons.dark_mode_outlined),
                  ),
                ],
                selected: {theme},
                onSelectionChanged: (v) => setState(() => theme = v.first),
              ),
              const SizedBox(height: 24),
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Expanded(
                    child: TextFormField(
                      controller: port,
                      decoration: const InputDecoration(
                        labelText: '本地路由埠號',
                        helperText: '預設 15722；0 代表動態埠',
                      ),
                      validator: (v) {
                        final p = int.tryParse(v!);
                        return p == null || p < 0 || p > 65535
                            ? '請輸入 0 到 65535'
                            : null;
                      },
                    ),
                  ),
                  const SizedBox(width: 20),
                  Expanded(
                    child: TextFormField(
                      controller: retention,
                      decoration: const InputDecoration(
                        labelText: '紀錄保留天數',
                        helperText: '1 到 3650 天',
                      ),
                      validator: (v) {
                        final p = int.tryParse(v!);
                        return p == null || p < 1 || p > 3650
                            ? '請輸入 1 到 3650'
                            : null;
                      },
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 24),
              AsyncButton(
                label: '保存偏好',
                icon: Icons.save_outlined,
                filled: true,
                onPressed: () async {
                  if (!form.currentState!.validate()) return;
                  final result = await command(context, ref, 'settings.save', {
                    'theme': theme,
                    'port': int.parse(port.text),
                    'retentionDays': int.parse(retention.text),
                  });
                  if (result != null && context.mounted) {
                    toast(context, '偏好已保存');
                  }
                },
              ),
            ],
          ),
        ),
      ),
      const SizedBox(height: 20),
      SectionCard(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('備份與還原', style: Theme.of(context).textTheme.titleLarge),
            const Notice(
              '匯出包含連線、模型、客戶端選擇與偏好，不包含 API Key 或原始設定快照。匯入只恢復選擇，不會自動接管客戶端。',
            ),
            Wrap(
              spacing: 12,
              runSpacing: 12,
              children: [
                AsyncButton(
                  label: '匯出設定',
                  icon: Icons.file_upload_outlined,
                  onPressed: () async {
                    final result = await command(context, ref, 'backup.export');
                    if (result == null) return;
                    final location = await getSaveLocation(
                      suggestedName: 'qc-switch-backup.json',
                      acceptedTypeGroups: [
                        const XTypeGroup(label: 'JSON', extensions: ['json']),
                      ],
                    );
                    if (location == null) return;
                    try {
                      await File(location.path).writeAsString(
                        const JsonEncoder.withIndent('  ').convert(result),
                        flush: true,
                      );
                      if (context.mounted) toast(context, '已匯出設定（不含憑證）');
                    } catch (e) {
                      if (context.mounted) toast(context, '無法寫入備份：$e');
                    }
                  },
                ),
                AsyncButton(
                  label: '匯入設定',
                  icon: Icons.file_download_outlined,
                  onPressed: () async {
                    final file = await openFile(
                      acceptedTypeGroups: [
                        const XTypeGroup(label: 'JSON', extensions: ['json']),
                      ],
                    );
                    if (file == null) return;
                    try {
                      if (await file.length() > 8 * 1024 * 1024) {
                        throw const FormatException('備份檔案過大');
                      }
                      final data = object(
                        jsonDecode(await file.readAsString()),
                      );
                      if (!context.mounted ||
                          !await confirm(
                            context,
                            '匯入設定？',
                            '相同識別碼的連線會被更新；現有 Key 會保留。請先解除客戶端接管。',
                            action: '匯入',
                          )) {
                        return;
                      }
                      if (context.mounted) {
                        await command(context, ref, 'backup.import', data);
                      }
                    } catch (e) {
                      if (context.mounted) toast(context, '無法讀取備份：$e');
                    }
                  },
                ),
              ],
            ),
          ],
        ),
      ),
      const SizedBox(height: 20),
      SectionCard(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              'QC Switch ${widget.state['version']} · Portable',
              style: Theme.of(context).textTheme.titleMedium,
            ),
            const SizedBox(height: 12),
            SelectableText(
              '資料位置：${widget.state['dataDirectory']}',
              style: const TextStyle(height: 1.6),
            ),
            const Notice(
              'Key 與恢復快照使用 Windows DPAPI 加密，僅原 Windows 使用者可解密。搬到其他帳戶時，請匯入設定並重新輸入 Key。',
              icon: Icons.lock_outline,
            ),
            const Notice(
              '關閉視窗後留在系統匣；選擇「退出」會先還原客戶端設定。開機自啟預設關閉，本版不寫入自啟項目。',
              icon: Icons.desktop_windows_outlined,
            ),
            TextButton.icon(
              onPressed: () => showLicensePage(
                context: context,
                applicationName: 'QC Switch',
                applicationVersion: '${widget.state['version']}',
                applicationLegalese: 'MIT · cc-switch attribution and Go dependency licenses are included in the portable folder.',
              ),
              icon: const Icon(Icons.description_outlined),
              label: const Text('第三方授權'),
            ),
          ],
        ),
      ),
    ],
  );
}
