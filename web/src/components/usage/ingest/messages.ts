export const pushIngestMessages = {
  "en": {
    "title": "Central usage receiver",
    "pending": "Pending: {{count}}",
    "quarantined": "Needs attention: {{count}}",
    "last_received": "Last received",
    "inspect": "Review processing errors",
    "quarantine_help": "After resolving the underlying issue, retry retained records here. Showing up to 100 records.",
    "empty": "No records need attention.",
    "attempts": "Attempts: {{count}}",
    "retry": "Retry processing",
    "load_error": "Could not load retained records.",
    "retry_error": "Could not retry processing."
  },
  "zh": {
    "title": "集中用量接收",
    "pending": "待处理：{{count}}",
    "quarantined": "需要处理：{{count}}",
    "last_received": "最近接收",
    "inspect": "查看处理异常",
    "quarantine_help": "修复导致处理失败的问题后，可在此重新处理保留的记录。最多显示 100 条。",
    "empty": "暂无需要处理的记录。",
    "attempts": "已尝试：{{count}} 次",
    "retry": "重新处理",
    "load_error": "无法加载保留的记录。",
    "retry_error": "无法重新处理记录。"
  },
  "zh-TW": {
    "title": "集中用量接收",
    "pending": "待處理：{{count}}",
    "quarantined": "需要處理：{{count}}",
    "last_received": "最近接收",
    "inspect": "查看處理異常",
    "quarantine_help": "修復導致處理失敗的問題後，可在此重新處理保留的記錄。最多顯示 100 筆。",
    "empty": "暫無需要處理的記錄。",
    "attempts": "已嘗試：{{count}} 次",
    "retry": "重新處理",
    "load_error": "無法載入保留的記錄。",
    "retry_error": "無法重新處理記錄。"
  }
} as const;
