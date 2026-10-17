import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:window_manager/window_manager.dart';

import 'core.dart';
import 'state.dart';
import 'shell.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await windowManager.ensureInitialized();
  await windowManager.waitUntilReadyToShow(
    const WindowOptions(
      size: Size(1240, 820),
      minimumSize: Size(960, 640),
      center: true,
      title: 'QC Switch',
    ),
    () async {
      await windowManager.show();
      await windowManager.focus();
    },
  );
  final core = CoreClient();
  try {
    await core.start();
  } catch (e) {
    core.failure = e.toString();
  }
  runApp(
    ProviderScope(
      overrides: [coreProvider.overrideWithValue(core)],
      child: const QCSwitchApp(),
    ),
  );
}

class QCSwitchApp extends ConsumerWidget {
  const QCSwitchApp({super.key, this.nativeIntegration = true});
  final bool nativeIntegration;
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = ref
        .watch(appStateProvider)
        .asData
        ?.value['settings']?['theme'];
    ThemeData makeTheme(Brightness brightness) {
      final colors =
          ColorScheme.fromSeed(
            seedColor: const Color(0xff006b63),
            brightness: brightness,
          ).copyWith(
            surface: brightness == Brightness.dark
                ? const Color(0xff141618)
                : const Color(0xfff8f9fa),
            surfaceContainerLow: brightness == Brightness.dark
                ? const Color(0xff1c1e21)
                : Colors.white,
            surfaceContainerHighest: brightness == Brightness.dark
                ? const Color(0xff2b2e32)
                : const Color(0xffeef0f2),
            outlineVariant: brightness == Brightness.dark
                ? const Color(0xff35393e)
                : const Color(0xffe2e5e8),
          );
      return ThemeData(
        useMaterial3: true,
        colorScheme: colors,
        fontFamily: 'Microsoft JhengHei',
        scaffoldBackgroundColor: colors.surface,
        inputDecorationTheme: InputDecorationTheme(
          border: OutlineInputBorder(borderRadius: BorderRadius.circular(12)),
          enabledBorder: OutlineInputBorder(
            borderRadius: BorderRadius.circular(12),
            borderSide: BorderSide(color: colors.outlineVariant),
          ),
          contentPadding: const EdgeInsets.symmetric(
            horizontal: 16,
            vertical: 18,
          ),
          isDense: true,
        ),
        cardTheme: CardThemeData(
          elevation: 0,
          margin: EdgeInsets.zero,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(16),
            side: BorderSide(color: colors.outlineVariant),
          ),
        ),
        filledButtonTheme: FilledButtonThemeData(
          style: FilledButton.styleFrom(
            padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 16),
          ),
        ),
      );
    }

    return MaterialApp(
      title: 'QC Switch',
      debugShowCheckedModeBanner: false,
      locale: const Locale('zh', 'TW'),
      supportedLocales: const [Locale('zh', 'TW')],
      localizationsDelegates: GlobalMaterialLocalizations.delegates,
      theme: makeTheme(Brightness.light),
      darkTheme: makeTheme(Brightness.dark),
      themeMode: theme == 'light'
          ? ThemeMode.light
          : theme == 'dark'
          ? ThemeMode.dark
          : ThemeMode.system,
      home: AppShell(nativeIntegration: nativeIntegration),
    );
  }
}
