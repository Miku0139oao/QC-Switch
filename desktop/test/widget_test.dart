import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:qc_switch/core.dart';
import 'package:qc_switch/main.dart';
import 'package:qc_switch/profiles.dart';
import 'package:qc_switch/state.dart';
import 'package:qc_switch/widgets.dart';

Json fixture() => object(
  jsonDecode('''{
  "version":"0.1.0", "contract":1, "running":false, "active":0,
  "address":"", "records":[], "recovery":[], "dataDirectory":"X:/portable/user-data",
  "settings":{"port":15722,"theme":"system","retentionDays":30},
  "profiles":[{"id":"main","name":"測試連線","endpoint":"https://api.quickrouter.ai",
    "protocol":"responses","hasKey":true,"models":[{"id":"test-model","name":"測試模型",
    "tools":true,"vision":true,"reasoning":false,"context":32000}]}],
  "clients":[
    {"client":"claude","path":"X:/isolated/claude/settings.json","managed":false,"warnings":[]},
    {"client":"codex","path":"X:/isolated/codex/config.toml","managed":false,"warnings":[],
     "binding":{"client":"codex","profileId":"main","model":"test-model","mode":"direct","directory":"","roles":{}}}
  ]
}'''),
);

class FakeCore implements RpcClient {
  Json state = fixture();
  final calls = <(String, Json?)>[];
  final events = StreamController<void>.broadcast();
  @override
  Stream<void> get changes => events.stream;
  @override
  Future<dynamic> call(String method, [Json? params]) async {
    calls.add((method, params));
    if (method == 'connection.test') {
      return {'kind': params!['kind'], 'ok': true, 'message': '收到預期回應'};
    }
    if (method == 'profiles.save') {
      final profiles = objects(state['profiles']);
      final existing = profiles
          .where((p) => p['id'] == params!['id'])
          .firstOrNull;
      state['profiles'] = [
        for (final p in profiles)
          if (p['id'] != params!['id']) p,
        {
          ...params!,
          'hasKey':
              params['key']?.isNotEmpty == true || existing?['hasKey'] == true,
        }..remove('key'),
      ];
    }
    if (method == 'clients.apply') {
      final clients = objects(state['clients']);
      final client = clients.firstWhere(
        (c) => c['client'] == params!['client'],
      );
      client['managed'] = true;
      client['binding'] = params;
      state['clients'] = clients;
    }
    return state;
  }
}

Future<FakeCore> launch(
  WidgetTester tester, {
  Size size = const Size(1240, 820),
  Json? initialState,
}) async {
  tester.view.devicePixelRatio = 1;
  tester.view.physicalSize = size;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  final core = FakeCore();
  if (initialState != null) core.state = initialState;
  addTearDown(core.events.close);
  await tester.pumpWidget(
    ProviderScope(
      overrides: [coreProvider.overrideWithValue(core)],
      child: const QCSwitchApp(nativeIntegration: false),
    ),
  );
  await tester.pumpAndSettle();
  return core;
}

void main() {
  Future<void> openCodex(WidgetTester tester) async {
    await tester.tap(
      find.descendant(
        of: find.byType(NavigationRail),
        matching: find.text('Codex'),
      ),
    );
    await tester.pumpAndSettle();
  }

  FilledButton applyButton(WidgetTester tester, String label) =>
      tester.widget<FilledButton>(find.widgetWithText(FilledButton, label));

  testWidgets(
    'new profiles use the API host and website correction preserves stored credentials',
    (tester) async {
      final data = fixture();
      data['profiles'][0]['endpoint'] = 'https://quickrouter.ai/v1/';
      final core = await launch(tester, initialState: data);
      await openCodex(tester);
      expect(find.text('改用 API 位址'), findsOneWidget);
      expect(applyButton(tester, '套用直連設定').onPressed, isNull);
      await tester.tap(find.text('改用 API 位址'));
      await tester.pumpAndSettle();
      final saved = core.calls.singleWhere((c) => c.$1 == 'profiles.save').$2!;
      expect(saved['endpoint'], 'https://api.quickrouter.ai');
      expect(saved.containsKey('key'), isFalse);
      expect(saved['models'], isNotEmpty);
      expect(applyButton(tester, '套用直連設定').onPressed, isNotNull);
      expect(
        core.calls.where(
          (c) =>
              c.$1 == 'models.fetch' ||
              c.$1 == 'connection.test' ||
              c.$1 == 'clients.apply',
        ),
        isEmpty,
      );
      await tester.tap(find.text('連線設定'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(FilledButton, '新增連線').first);
      await tester.pumpAndSettle();
      final endpoint = tester.widget<TextFormField>(
        find.byType(TextFormField).at(1),
      );
      expect(endpoint.controller!.text, 'https://api.quickrouter.ai');
      expect(
        quickRouterApiSuggestion('https://quickrouter.us'),
        'https://api.quickrouter.us',
      );
      expect(quickRouterApiSuggestion('https://custom.example/v1'), isNull);
    },
  );

  testWidgets(
    'three saved profiles without binding can select and enable a compatible route',
    (tester) async {
      final data = fixture();
      final main = objects(data['profiles']).single;
      data['profiles'] = [
        main,
        {...main, 'id': 'chat', 'name': 'Chat 連線', 'protocol': 'chat'},
        {
          ...main,
          'id': 'messages',
          'name': 'Messages 連線',
          'protocol': 'messages',
        },
      ];
      data['clients'][1].remove('binding');
      final core = await launch(tester, initialState: data);
      await openCodex(tester);
      expect(find.text('請先選擇一組 QuickRouter 連線。'), findsOneWidget);
      expect(applyButton(tester, '套用直連設定').onPressed, isNull);
      await tester.tap(find.byType(DropdownButtonFormField<String>).first);
      await tester.pumpAndSettle();
      await tester.tap(find.text('Chat 連線 · Chat Completions').last);
      await tester.pumpAndSettle();
      expect(applyButton(tester, '啟用本地路由').onPressed, isNotNull);
      // Status events do not discard the user's pending selection.
      core.events.add(null);
      await tester.pump(const Duration(milliseconds: 400));
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.text('啟用本地路由'));
      await tester.tap(find.text('啟用本地路由'));
      await tester.pumpAndSettle();
      final binding = core.calls
          .singleWhere((c) => c.$1 == 'clients.apply')
          .$2!;
      expect(binding['profileId'], 'chat');
      expect(binding['mode'], 'route');
      expect(binding['model'], 'test-model');
      expect(
        core.calls.where(
          (c) => c.$1 == 'connection.test' || c.$1.startsWith('gateway.'),
        ),
        isEmpty,
      );
    },
  );

  testWidgets(
    'missing models can be added from the client page without a probe',
    (tester) async {
      final data = fixture();
      data['profiles'][0]['models'] = [];
      final core = await launch(tester, initialState: data);
      await openCodex(tester);
      expect(find.textContaining('此連線尚未加入模型'), findsOneWidget);
      expect(applyButton(tester, '套用直連設定').onPressed, isNull);
      await tester.tap(find.text('手動新增模型'));
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextFormField).first, 'chosen-model');
      await tester.tap(find.text('保存模型'));
      await tester.pumpAndSettle();
      expect(applyButton(tester, '套用直連設定').onPressed, isNotNull);
      expect(
        core.calls
            .singleWhere((c) => c.$1 == 'profiles.save')
            .$2!['models'][0]['id'],
        'chosen-model',
      );
      expect(
        core.calls.where(
          (c) =>
              c.$1 == 'models.fetch' ||
              c.$1 == 'connection.test' ||
              c.$1 == 'clients.apply',
        ),
        isEmpty,
      );
      core.state['profiles'][0]['hasKey'] = false;
      core.events.add(null);
      await tester.pump(const Duration(milliseconds: 400));
      await tester.pumpAndSettle();
      expect(find.textContaining('尚未保存 API Key'), findsOneWidget);
      expect(applyButton(tester, '套用直連設定').onPressed, isNull);
    },
  );

  testWidgets(
    'external binding and model changes reconcile the open client form',
    (tester) async {
      final core = await launch(tester);
      await openCodex(tester);
      core.state['profiles'][0]['models'].add({
        'id': 'other-model',
        'tools': true,
      });
      core.state['clients'][1]['binding']['model'] = 'other-model';
      core.events.add(null);
      await tester.pump(const Duration(milliseconds: 400));
      await tester.pumpAndSettle();
      expect(
        tester
            .widget<DropdownButtonFormField<String>>(
              find.byType(DropdownButtonFormField<String>).at(1),
            )
            .initialValue,
        'other-model',
      );
      core.state['profiles'][0]['models'].removeLast();
      core.events.add(null);
      await tester.pump(const Duration(milliseconds: 400));
      await tester.pumpAndSettle();
      expect(find.text('請選擇預設模型，即可啟用客戶端。'), findsOneWidget);
      expect(applyButton(tester, '套用直連設定').onPressed, isNull);
      expect(tester.takeException(), isNull);
    },
  );

  for (final size in [const Size(1240, 820), const Size(960, 640)]) {
    testWidgets('all pages fit $size without starting routing or probes', (
      tester,
    ) async {
      final core = await launch(tester, size: size);
      expect(find.text('未監聽'), findsOneWidget);
      for (final label in [
        '連線設定',
        'Claude Code',
        'Codex',
        '用量紀錄',
        '設定',
        '總覽',
      ]) {
        await tester.tap(
          find.descendant(
            of: find.byType(NavigationRail),
            matching: find.text(label),
          ),
        );
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull, reason: label);
      }
      expect(core.calls.every((c) => c.$1 == 'initialize'), isTrue);
    });
  }

  testWidgets(
    'profile edit preserves a masked key and requires explicit save',
    (tester) async {
      final core = await launch(tester);
      await tester.tap(find.text('連線設定'));
      await tester.pumpAndSettle();
      await tester.tap(find.byTooltip('編輯連線'));
      await tester.pumpAndSettle();
      expect(core.calls.where((c) => c.$1 == 'profiles.save'), isEmpty);
      final fields = find.byType(TextFormField);
      await tester.enterText(fields.first, '新連線名稱');
      final keyField = tester.widget<TextField>(
        find.descendant(of: fields.at(2), matching: find.byType(TextField)),
      );
      expect(keyField.obscureText, isTrue);
      await tester.tap(find.text('保存連線'));
      await tester.pumpAndSettle();
      final saved = core.calls.singleWhere((c) => c.$1 == 'profiles.save').$2!;
      expect(saved['name'], '新連線名稱');
      expect(saved['key'], '');
      expect(find.text('新連線名稱'), findsOneWidget);
    },
  );

  testWidgets(
    'direct configuration submits the selected client without starting gateway',
    (tester) async {
      final core = await launch(tester);
      await tester.tap(
        find.descendant(
          of: find.byType(NavigationRail),
          matching: find.text('Codex'),
        ),
      );
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.text('套用直連設定'));
      await tester.tap(find.text('套用直連設定'));
      await tester.pumpAndSettle();
      final binding = core.calls
          .singleWhere((c) => c.$1 == 'clients.apply')
          .$2!;
      expect(binding['mode'], 'direct');
      expect(binding['client'], 'codex');
      expect(binding['model'], 'test-model');
      expect(core.calls.where((c) => c.$1.startsWith('gateway.')), isEmpty);
    },
  );

  testWidgets('probes only run on explicit action and report separately', (
    tester,
  ) async {
    final core = await launch(tester);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [coreProvider.overrideWithValue(core)],
        child: MaterialApp(
          home: Scaffold(
            body: ProbeDialog(profile: objects(core.state['profiles']).first),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.textContaining('可能產生費用'), findsOneWidget);
    expect(core.calls.where((c) => c.$1 == 'connection.test'), isEmpty);
    await tester.tap(find.text('工具呼叫'));
    await tester.pumpAndSettle();
    final probe = core.calls.singleWhere((c) => c.$1 == 'connection.test').$2!;
    expect(probe, {'id': 'main', 'model': 'test-model', 'kind': 'tools'});
    expect(find.text('尚未測試'), findsNWidgets(3));
    expect(find.text('收到預期回應'), findsOneWidget);
  });

  testWidgets('conflict restoration requires an individual choice', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: ConflictDialog(
            conflicts: [
              {
                'field': 'model',
                'current': 'external',
                'original': 'old',
                'managed': 'ours',
              },
            ],
          ),
        ),
      ),
    );
    final button = find.widgetWithText(FilledButton, '套用選擇並還原');
    expect(tester.widget<FilledButton>(button).onPressed, isNull);
    await tester.tap(find.text('保留目前值'));
    await tester.pumpAndSettle();
    expect(tester.widget<FilledButton>(button).onPressed, isNotNull);
  });

  testWidgets('core notifications refresh actual state and theme', (
    tester,
  ) async {
    final core = await launch(tester);
    core.state['running'] = true;
    core.state['address'] = 'http://127.0.0.1:15722';
    core.state['settings']['theme'] = 'dark';
    core.events.add(null);
    await tester.pump(const Duration(milliseconds: 400));
    await tester.pumpAndSettle();
    expect(find.text('本地路由運作中'), findsOneWidget);
    expect(core.calls.where((c) => c.$1 == 'state.get'), hasLength(1));
    expect(
      Theme.of(tester.element(find.byType(NavigationRail))).brightness,
      Brightness.dark,
    );
  });
}
