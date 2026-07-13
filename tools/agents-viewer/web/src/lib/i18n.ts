import i18n from "i18next"
import { initReactI18next } from "react-i18next"

export const resources = {
  en: { translation: {
    appName: "Agents Viewer", sessions: "Sessions", search: "Search", searchPlaceholder: "Search conversations…",
    noSessions: "No sessions yet", noEntries: "This session has no displayable entries", loading: "Loading…",
    archived: "Archive", archiveActive: "Active sessions", archiveInclude: "Active and archived", archiveOnly: "Archived only", archiveHelp: "Codex CLI manages archive state with codex archive and codex unarchive. This viewer remains read-only.", source: "Source", allSources: "All sources", sourceHelp: "What do these sources mean?", cwd: "Working directory", cwdPlaceholder: "Filter by working directory", inspector: "Inspector", raw: "Raw records", filter: "Filter", filterHelp: "Choose which sessions and conversation activity to show. Changes apply together.", filterActive: "Filter, {{count}} active", sessionFilters: "Sessions", conversationDisplay: "Conversation display", reset: "Reset", cancel: "Cancel", apply: "Apply",
    close: "Close", openNavigation: "Open sessions", openInspector: "Open inspector", closeInspector: "Close inspector", theme: "Theme", language: "Language",
    light: "Light", dark: "Dark", system: "System", english: "English", chinese: "简体中文", skip: "Skip to conversation",
    newContent: "{{count}} new items", copy: "Copy", copyMessage: "Copy message", copying: "Copying…", copied: "Copied", copyFailed: "Copy failed", retry: "Retry", partial: "Results may be incomplete", conversationNavigation: "Conversation navigation", jumpTop: "Go to first message", jumpBottom: "Go to latest message", jumpBottomNew: "Go to {{count}} new items",
    today: "Today", yesterday: "Yesterday", last7: "Last 7 Days", older: "Older", untitled: "Untitled",
    user: "User", assistant: "Assistant", developer: "Developer", systemRole: "System", reasoning: "Reasoning", tool: "Tool",
    context: "Context", details: "Technical details", status: "Index status", emptySearch: "No matching entries", noPreview: "No conversation preview",
    searchHelp: "Type at least one character. Press Enter to open a result.", searchAllTypes: "Search all activity types", searchAllTypesHelp: "Also search reasoning, commands, context, and other technical activity.", back: "Back", diagnostics: "Diagnostics",
    primary: "Primary content", secondary: "Secondary content", loadMore: "Load more", menu: "Menu", entryCount: "{{count}} entries", attachment: "Attachment", image: "image", unknownError: "Unknown error",
    allHistory: "All history", newOnly: "New only", dayWindow: "{{count}}d", excludedCount: "{{count}} excluded", indexCutoff: "Effective cutoff: {{cutoff}}; excluded data: {{bytes}}", none: "none",
    showTechnical: "Show technical activity", showTechnicalHelp: "Shows turn context, patches, tools, and other internal activity.", showTechnicalForced: "Temporarily enabled to reveal the linked technical entry.", commandUnavailable: "command details unavailable", inputContent: "Input", outputContent: "Result", warning: "Warning", errorLabel: "Error", received: "Received", technical: "Technical", internal: "Internal",
    indexDiscovering: "Discovering conversations…", indexIndexing: "Indexing {{processed}} / {{total}}", indexReady: "Index ready", indexDegraded: "Index completed with {{count}} failures", inspectorEmpty: "Choose Inspect on an entry to view technical details.",
    sourceCli: "Codex CLI", sourceCliHelp: "Interactive CLI/TUI conversations.", sourceVscode: "VS Code extension", sourceVscodeHelp: "Conversations started by the Codex VS Code extension.", sourceExec: "Codex Exec", sourceExecHelp: "Non-interactive codex exec tasks.", sourceReview: "Code review", sourceReviewHelp: "Review tasks created as specialized child sessions.", sourceSubagent: "Sub-agent", sourceSubagentHelp: "Child agent sessions started by another conversation.", sourceAppServer: "App Server / integration", sourceAppServerHelp: "Sessions started through app-server, MCP, or another integration client.", sourceUnknown: "Unknown source", sourceUnknownHelp: "The rollout metadata was insufficient to identify the origin."
  }},
  "zh-CN": { translation: {
    appName: "Agents Viewer", sessions: "会话", search: "搜索", searchPlaceholder: "搜索会话内容…",
    noSessions: "暂无会话", noEntries: "此会话没有可显示条目", loading: "加载中…",
    archived: "归档", archiveActive: "活跃会话", archiveInclude: "活跃及已归档", archiveOnly: "仅已归档", archiveHelp: "归档状态由 Codex CLI 的 codex archive 和 codex unarchive 管理；查看器始终只读。", source: "来源", allSources: "全部来源", sourceHelp: "这些来源分别是什么意思？", cwd: "工作目录", cwdPlaceholder: "按工作目录筛选", inspector: "检查器", raw: "原始记录", filter: "筛选", filterHelp: "选择要显示的会话和对话活动，所有改动会一次生效。", filterActive: "筛选，已启用 {{count}} 项", sessionFilters: "会话", conversationDisplay: "对话显示", reset: "重置", cancel: "取消", apply: "应用",
    close: "关闭", openNavigation: "打开会话列表", openInspector: "打开检查器", closeInspector: "关闭检查器", theme: "主题", language: "语言",
    light: "浅色", dark: "深色", system: "跟随系统", english: "English", chinese: "简体中文", skip: "跳到会话正文",
    newContent: "{{count}} 条新内容", copy: "复制", copyMessage: "复制消息", copying: "正在复制…", copied: "已复制", copyFailed: "复制失败", retry: "重试", partial: "结果可能不完整", conversationNavigation: "对话导航", jumpTop: "到第一条消息", jumpBottom: "到最新消息", jumpBottomNew: "查看 {{count}} 条新内容",
    today: "今天", yesterday: "昨天", last7: "最近 7 天", older: "更早", untitled: "未命名",
    user: "用户", assistant: "助手", developer: "开发者", systemRole: "系统", reasoning: "推理摘要", tool: "工具",
    context: "上下文", details: "技术详情", status: "索引状态", emptySearch: "没有匹配条目", noPreview: "暂无对话预览",
    searchHelp: "输入至少一个字符，按回车打开结果。", searchAllTypes: "搜索所有活动类型", searchAllTypesHelp: "同时搜索推理、命令、上下文和其他技术活动。", back: "返回", diagnostics: "诊断",
    primary: "主要内容", secondary: "次要内容", loadMore: "加载更多", menu: "菜单", entryCount: "{{count}} 条", attachment: "附件", image: "图片", unknownError: "未知错误",
    allHistory: "全部历史", newOnly: "仅新会话", dayWindow: "{{count}} 天", excludedCount: "已排除 {{count}} 个", indexCutoff: "生效截止时间：{{cutoff}}；已排除数据：{{bytes}}", none: "无",
    showTechnical: "显示技术活动", showTechnicalHelp: "显示轮次上下文、补丁、工具和其他内部活动。", showTechnicalForced: "已临时启用，以显示链接指向的技术条目。", commandUnavailable: "命令内容不可用", inputContent: "输入", outputContent: "执行结果", warning: "警告", errorLabel: "错误", received: "接收", technical: "技术项", internal: "内部",
    indexDiscovering: "正在发现会话…", indexIndexing: "正在索引 {{processed}} / {{total}}", indexReady: "索引就绪", indexDegraded: "索引完成，{{count}} 个失败", inspectorEmpty: "点击条目上的“检查”查看技术详情。",
    sourceCli: "Codex CLI", sourceCliHelp: "Codex CLI/TUI 的交互式会话。", sourceVscode: "VS Code 扩展", sourceVscodeHelp: "由 Codex VS Code 扩展启动的会话。", sourceExec: "Codex Exec", sourceExecHelp: "由 codex exec 启动的非交互任务。", sourceReview: "代码审查", sourceReviewHelp: "作为专用子会话运行的审查任务。", sourceSubagent: "子代理", sourceSubagentHelp: "由另一个会话启动的子代理会话。", sourceAppServer: "App Server / 集成", sourceAppServerHelp: "由 app-server、MCP 或其他集成客户端启动的会话。", sourceUnknown: "未知来源", sourceUnknownHelp: "rollout 元数据不足，无法可靠判断来源。"
  }}
} as const

const storedLanguage = localStorage.getItem("agents-viewer-language")
const language = storedLanguage ?? (navigator.language.toLowerCase().startsWith("zh") ? "zh-CN" : "en")

void i18n.use(initReactI18next).init({ resources, lng: language, fallbackLng: "en", interpolation: { escapeValue: false } })
document.documentElement.lang = language

export function setLanguage(language: "en" | "zh-CN") {
  localStorage.setItem("agents-viewer-language", language)
  document.documentElement.lang = language
  void i18n.changeLanguage(language)
}

export default i18n
