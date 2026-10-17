import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:tray_manager/tray_manager.dart';
import 'package:url_launcher/url_launcher.dart';
import 'package:window_manager/window_manager.dart';

import 'clients.dart';
import 'core.dart';
import 'pages.dart';
import 'profiles.dart';
import 'state.dart';
import 'widgets.dart';

class AppShell extends ConsumerStatefulWidget {
  const AppShell({super.key, this.nativeIntegration = true});
  final bool nativeIntegration;
  @override
  ConsumerState<AppShell> createState() => _AppShellState();
}

class _AppShellState extends ConsumerState<AppShell>
    with WindowListener, TrayListener {
  int page = 0;
  bool trayReady = false, quitting = false;
  String traySignature = '';
  @override
  void initState() {
    super.initState();
    if (widget.nativeIntegration) {
      windowManager.addListener(this);
      trayManager.addListener(this);
      unawaited(_setupTray());
    }
  }

  Future<void> _setupTray() async {
    try {
      await trayManager.setIcon('assets/tray.ico');
      await trayManager.setToolTip('QC Switch');
      await windowManager.setPreventClose(true);
      trayReady = true;
      _updateTray(ref.read(appStateProvider).asData?.value ?? {});
    } catch (_) {
      if (mounted) toast(context, '系統匣無法啟動，關閉視窗時會先還原設定並退出。');
      await windowManager.setPreventClose(true);
    }
  }

  @override
  void dispose() {
    if (widget.nativeIntegration) {
      windowManager.removeListener(this);
      trayManager.removeListener(this);
    }
    super.dispose();
  }

  void _updateTray(Json state) {
    if (!trayReady) return;
    final signature =
        '${state['running']}|${state['profiles']}|${state['clients']}';
    if (signature == traySignature) return;
    traySignature = signature;
    final menu = Menu(
      items: [
        MenuItem(key: 'show', label: '顯示 QC Switch'),
        MenuItem.separator(),
        MenuItem(
          key: 'route',
          label: state['running'] == true ? '停止本地路由並還原' : '啟用上次的本地路由',
        ),
        for (final client in objects(state['clients']).where(
          (c) =>
              c['managed'] == true && object(c['binding'])['mode'] == 'route',
        ))
          MenuItem.submenu(
            label: '${client['client']} 切換連線',
            submenu: Menu(
              items: [
                for (final p in objects(state['profiles']).where(
                  (p) => p['hasKey'] == true && objects(p['models']).isNotEmpty,
                ))
                  MenuItem(
                    key: 'switch:${client['client']}:${p['id']}',
                    label: '${p['name']}',
                  ),
              ],
            ),
          ),
        MenuItem.separator(),
        MenuItem(key: 'quit', label: '還原設定並退出'),
      ],
    );
    unawaited(trayManager.setContextMenu(menu));
  }

  Future<void> _show() async {
    await windowManager.show();
    await windowManager.focus();
  }

  @override
  void onWindowClose() {
    if (trayReady) {
      unawaited(windowManager.hide());
    } else {
      unawaited(_quit());
    }
  }

  @override
  void onTrayIconMouseDown() => unawaited(_show());
  @override
  void onTrayIconRightMouseDown() => unawaited(trayManager.popUpContextMenu());
  @override
  void onTrayMenuItemClick(MenuItem item) {
    unawaited(_trayAction(item.key ?? ''));
  }

  Future<void> _trayAction(String key) async {
    if (key == 'show') return _show();
    if (key == 'quit') return _quit();
    final state = ref.read(appStateProvider).asData?.value;
    if (state == null) return;
    if (key == 'route') {
      if (state['running'] == true) {
        await _show();
        if (mounted) await _stop();
      } else {
        final bindings = objects(state['clients'])
            .map((c) => object(c['binding']))
            .where((b) => b['mode'] == 'route' && b['profileId'] != null);
        if (bindings.isEmpty) {
          await _show();
          if (mounted) {
            setState(() => page = 3);
            toast(context, '請先選擇客戶端、連線與模型，再啟用本地路由。');
          }
        }
        for (final b in bindings) {
          if (mounted) await command(context, ref, 'clients.apply', b);
        }
      }
    } else if (key.startsWith('switch:')) {
      final parts = key.split(':');
      final c = objects(state['clients'])
          .firstWhere((c) => c['client'] == parts[1]);
      final p = objects(state['profiles'])
          .firstWhere((p) => p['id'] == parts[2]);
      final b = object(c['binding']);
      if (mounted) {
        await command(context, ref, 'clients.apply', {
          ...b,
          'profileId': p['id'],
          'model': objects(p['models']).first['id'],
          'roles': <String, dynamic>{},
        });
      }
    }
  }

  Future<void> _stop() async {
    final clients = objects(
      ref.read(appStateProvider).asData?.value['clients'],
    );
    for (final c in clients.where(
      (c) => c['managed'] == true && object(c['binding'])['mode'] == 'route',
    )) {
      if (!mounted ||
          await command(context, ref, 'clients.restore', {
                'client': c['client'],
              }) ==
              null) {
        return;
      }
    }
    if (mounted) await command(context, ref, 'gateway.stop');
  }

  Future<void> _quit() async {
    if (quitting) return;
    quitting = true;
    try {
      await _show();
      final snapshot = ref.read(appStateProvider).asData?.value;
      for (final c in objects(
        snapshot?['clients'],
      ).where((c) => c['managed'] == true)) {
        if (!mounted ||
            await command(context, ref, 'clients.restore', {
                  'client': c['client'],
                }) ==
                null) {
          return;
        }
      }
      if (!mounted) return;
      final result = await command(context, ref, 'shutdown');
      if (result == null) {
        final core = ref.read(coreProvider);
        if (core is CoreClient &&
            core.failure != null &&
            mounted &&
            await confirm(
              context,
              '核心已停止',
              '設定會在下次啟動時檢查恢復。現在關閉視窗？',
              action: '關閉',
            )) {
          await windowManager.destroy();
        }
        return;
      }
      if (trayReady) await trayManager.destroy();
      await windowManager.destroy();
    } finally {
      quitting = false;
    }
  }

  @override
  Widget build(BuildContext context) {
    final snapshot = ref.watch(appStateProvider);
    ref.listen(appStateProvider, (_, next) {
      if (next.asData?.value case final Json s) _updateTray(s);
    });
    final c = Theme.of(context).colorScheme;
    const destinations = [
      NavigationRailDestination(
        icon: Icon(Icons.dashboard_outlined),
        selectedIcon: Icon(Icons.dashboard),
        label: Text('總覽'),
      ),
      NavigationRailDestination(
        icon: Icon(Icons.hub_outlined),
        selectedIcon: Icon(Icons.hub),
        label: Text('連線設定'),
      ),
      NavigationRailDestination(
        icon: Icon(Icons.terminal_outlined),
        selectedIcon: Icon(Icons.terminal),
        label: Text('Claude Code'),
      ),
      NavigationRailDestination(icon: Icon(Icons.code), label: Text('Codex')),
      NavigationRailDestination(
        icon: Icon(Icons.receipt_long_outlined),
        selectedIcon: Icon(Icons.receipt_long),
        label: Text('用量紀錄'),
      ),
      NavigationRailDestination(
        icon: Icon(Icons.settings_outlined),
        selectedIcon: Icon(Icons.settings),
        label: Text('設定'),
      ),
    ];
    return Scaffold(
      body: Row(
        children: [
          Container(
            width: 220,
            color: c.surfaceContainerLow,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Padding(
                  padding: const EdgeInsets.fromLTRB(24, 32, 16, 28),
                  child: Row(
                    children: [
                      Container(
                        width: 36,
                        height: 36,
                        decoration: BoxDecoration(
                          color: c.primary,
                          borderRadius: BorderRadius.circular(12),
                        ),
                        child: Icon(
                          Icons.alt_route,
                          color: c.onPrimary,
                          size: 24,
                        ),
                      ),
                      const SizedBox(width: 12),
                      Flexible(
                        child: Text(
                          'QC Switch',
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            fontSize: 20,
                            color: c.onSurface,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
                Expanded(
                  child: NavigationRail(
                    extended: true,
                    minExtendedWidth: 220,
                    backgroundColor: Colors.transparent,
                    selectedIndex: page,
                    destinations: destinations,
                    onDestinationSelected: (v) => setState(() => page = v),
                  ),
                ),
                Padding(
                  padding: const EdgeInsets.all(24),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      const StatusPill('Windows · Portable'),
                      const SizedBox(height: 16),
                      Text(
                        'QuickRouter 控制中心',
                        style: TextStyle(
                          color: c.onSurfaceVariant,
                          fontSize: 12,
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
          Expanded(
            child: Column(
              children: [
                Container(
                  height: 72,
                  padding: const EdgeInsets.symmetric(horizontal: 32),
                  child: Row(
                    children: [
                      const Icon(Icons.lock_outline, size: 16),
                      const SizedBox(width: 8),
                      Text(
                        '本機管理',
                        style: TextStyle(
                          color: c.onSurfaceVariant,
                          fontSize: 12,
                        ),
                      ),
                      const Spacer(),
                      TextButton.icon(
                        onPressed: () async {
                          try {
                            await launchUrl(
                              Uri.parse('https://quickrouter.us/balance'),
                              mode: LaunchMode.externalApplication,
                            );
                          } catch (_) {
                            if (context.mounted) toast(context, '無法開啟瀏覽器');
                          }
                        },
                        icon: const Icon(Icons.open_in_new, size: 16),
                        label: const Text('官方餘額'),
                      ),
                      IconButton(
                        tooltip: '重新整理',
                        onPressed: () =>
                            ref.read(appStateProvider.notifier).refresh(),
                        icon: const Icon(Icons.refresh),
                      ),
                      if (widget.nativeIntegration)
                        IconButton(
                          tooltip: '還原設定並退出',
                          onPressed: _quit,
                          icon: const Icon(Icons.logout, size: 20),
                        ),
                    ],
                  ),
                ),
                Expanded(
                  child: snapshot.when(
                    loading: () =>
                        const Center(child: CircularProgressIndicator()),
                    error: (e, _) => Padding(
                      padding: const EdgeInsets.all(32),
                      child: EmptyState(
                        '核心無法連線',
                        '$e',
                        icon: Icons.link_off,
                        action: TextButton(
                          onPressed: () =>
                              ref.read(appStateProvider.notifier).refresh(),
                          child: const Text('重試'),
                        ),
                      ),
                    ),
                    data: (state) => SingleChildScrollView(
                      key: ValueKey(page),
                      padding: const EdgeInsets.fromLTRB(32, 20, 32, 40),
                      child: switch (page) {
                        0 => OverviewPage(
                          state: state,
                          navigate: (v) => setState(() => page = v),
                        ),
                        1 => ProfilesPage(state: state),
                        2 || 3 => ClientPage(
                          key: ValueKey(page),
                          client: page == 2 ? 'claude' : 'codex',
                          state: state,
                          onConnections: () => setState(() => page = 1),
                        ),
                        4 => UsagePage(state: state),
                        _ => SettingsPage(state: state),
                      },
                    ),
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
