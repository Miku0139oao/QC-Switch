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
  "profiles":[{"id":"main","name":"測試連線","endpoint":"https://quickrouter.us",
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
      state['profiles'] = [
        {...params!, 'hasKey': true}..remove('key'),
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
}) async {
  tester.view.devicePixelRatio = 1;
  tester.view.physicalSize = size;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  final core = FakeCore();
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
