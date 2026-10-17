import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';

/// Brand artwork from Simple Icons 14.15.0 (CC0); see THIRD_PARTY_NOTICES.
class ClientLogo extends StatelessWidget {
  const ClientLogo(this.client, {super.key, this.size = 24, this.tile = false});
  final String client;
  final double size;
  final bool tile;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final logo = SvgPicture.asset(
      'assets/${client == 'claude' ? 'anthropic' : 'openai'}.svg',
      width: size,
      height: size,
      colorFilter: ColorFilter.mode(colors.onSurface, BlendMode.srcIn),
      semanticsLabel: client == 'claude' ? 'Anthropic' : 'OpenAI',
    );
    return tile
        ? Container(
            padding: const EdgeInsets.all(14),
            decoration: BoxDecoration(
              color: colors.surfaceContainerHighest,
              borderRadius: BorderRadius.circular(16),
            ),
            child: logo,
          )
        : logo;
  }
}
