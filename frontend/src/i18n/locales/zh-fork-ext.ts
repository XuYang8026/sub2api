// <fork:proxy-circuit-breaker> + <fork:proxy-smart-import>
// Fork-only Chinese translations. Merged into the main zh locale by
// i18n/index.ts.

export default {
  admin: {
    accounts: {
      proxy_unavailable: '代理不可用'
    },
    proxies: {
      columns: {
        health: '健康'
      },
      health_status: {
        healthy: '健康',
        unhealthy: '不可用',
        unknown: '未探测',
        probing: '探测中'
      },
      probe_now: '立即探测',
      auto_detect_protocol: '自动探测',
      batch_import_result_title: '导入结果',
      batch_import_detected_protocol: '检测到协议：{protocol}',
      batch_import_latency_ms: '{latency}ms',
      // 导入结果状态标签（detect_failed = 已保存但协议为回落值，
      // 使用琥珀色 warning 徽章提示运维再核对）。
      batch_status: {
        created: '已创建',
        skipped: '已跳过',
        detect_failed: '自动探测失败',
        failed: '失败'
      }
    }
  }
}

// </fork>
