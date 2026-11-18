import 'package:flutter/material.dart';

/// 厂商识别色。与站点 `data/vendors.ts` 的小圆点语义一致：只用于标识厂商，
/// 不承载「成功 / 错误 / 交互」这些语义色。放在 core/theme，避免数据层
/// （Repository）依赖 UI 组件。
abstract final class VendorColors {
  static const anthropic = Color(0xFFF97316);
  static const deepseek = Color(0xFF06B6D4);
  static const openai = Color(0xFF8B5CF6);
  static const google = Color(0xFFF59E0B);
  static const alibaba = Color(0xFFEC4899);
  static const fallback = Color(0xFF2563EB);

  static Color forVendor(String vendor) {
    final lower = vendor.toLowerCase();
    if (lower.contains('anthropic') || lower.contains('claude')) {
      return anthropic;
    }
    if (lower.contains('deepseek')) return deepseek;
    if (lower.contains('openai') || lower.contains('gpt')) return openai;
    if (lower.contains('google') || lower.contains('gemini')) return google;
    if (lower.contains('alibaba') || lower.contains('qwen')) return alibaba;
    return fallback;
  }
}
