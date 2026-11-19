// Composes per-namespace message catalogs into one bundle per locale.
//
// Both locales are statically imported so they ship in the client bundle — the static
// export has no server to load them on demand, and bundle size is not a concern for a
// self-hosted tool. Add a namespace by importing its two JSON files and adding one line
// to each locale object below.

import type { Locale } from "@/i18n/config";

import enAgentEditor from "./locales/en/agentEditor.json";
import enAgents from "./locales/en/agents.json";
import enApprovalExecutionFocus from "./locales/en/approvalExecutionFocus.json";
import enApprovalRecords from "./locales/en/approvalRecords.json";
import enAssetDslSearch from "./locales/en/assetDslSearch.json";
import enAssetInterceptRulesEditor from "./locales/en/assetInterceptRulesEditor.json";
import enAssets from "./locales/en/assets.json";
import enAuth from "./locales/en/auth.json";
import enChat from "./locales/en/chat.json";
import enChrome from "./locales/en/chrome.json";
import enCommands from "./locales/en/commands.json";
import enCommon from "./locales/en/common.json";
import enCopyButton from "./locales/en/copyButton.json";
import enDashboard from "./locales/en/dashboard.json";
import enDateRangePicker from "./locales/en/dateRangePicker.json";
import enExplorationGraph from "./locales/en/explorationGraph.json";
import enFindingRetestDialog from "./locales/en/findingRetestDialog.json";
import enFindingRetestPanel from "./locales/en/findingRetestPanel.json";
import enFindings from "./locales/en/findings.json";
import enFindingsComponents from "./locales/en/findingsComponents.json";
import enFindingsDetail from "./locales/en/findingsDetail.json";
import enFindingTrafficPanel from "./locales/en/findingTrafficPanel.json";
import enHttpCodeBlock from "./locales/en/httpCodeBlock.json";
import enIntercept from "./locales/en/intercept.json";
import enLanguageToggle from "./locales/en/languageToggle.json";
import enLinkTrafficDialog from "./locales/en/linkTrafficDialog.json";
import enLlm from "./locales/en/llm.json";
import enLlmRecords from "./locales/en/llmRecords.json";
import enLogs from "./locales/en/logs.json";
import enMcp from "./locales/en/mcp.json";
import enMentionTextarea from "./locales/en/mentionTextarea.json";
import enNav from "./locales/en/nav.json";
import enNotFound from "./locales/en/notFound.json";
import enNotify from "./locales/en/notify.json";
import enScopeTextEditor from "./locales/en/scopeTextEditor.json";
import enSettings from "./locales/en/settings.json";
import enSideQuestionWorkspace from "./locales/en/sideQuestionWorkspace.json";
import enSkills from "./locales/en/skills.json";
import enStatus from "./locales/en/status.json";
import enSync from "./locales/en/sync.json";
import enTablePagination from "./locales/en/tablePagination.json";
import enTaskAssets from "./locales/en/taskAssets.json";
import enTaskLlmProfileChain from "./locales/en/taskLlmProfileChain.json";
import enTasks from "./locales/en/tasks.json";
import enTasksAssets from "./locales/en/tasksAssets.json";
import enTasksBroadcast from "./locales/en/tasksBroadcast.json";
import enTasksCoverage from "./locales/en/tasksCoverage.json";
import enTasksDetail from "./locales/en/tasksDetail.json";
import enTasksMisc from "./locales/en/tasksMisc.json";
import enTasksOverview from "./locales/en/tasksOverview.json";
import enTasksSessions from "./locales/en/tasksSessions.json";
import enTaskTemplateControls from "./locales/en/taskTemplateControls.json";
import enTodoPopover from "./locales/en/todoPopover.json";
import enTools from "./locales/en/tools.json";
import enTraffic from "./locales/en/traffic.json";
import enTrafficEvidenceViewer from "./locales/en/trafficEvidenceViewer.json";
import enTrafficPickerDialog from "./locales/en/trafficPickerDialog.json";
import enTranscript from "./locales/en/transcript.json";
import enWorkspace from "./locales/en/workspace.json";
import koAgentEditor from "./locales/ko/agentEditor.json";
import koAgents from "./locales/ko/agents.json";
import koApprovalExecutionFocus from "./locales/ko/approvalExecutionFocus.json";
import koApprovalRecords from "./locales/ko/approvalRecords.json";
import koAssetDslSearch from "./locales/ko/assetDslSearch.json";
import koAssetInterceptRulesEditor from "./locales/ko/assetInterceptRulesEditor.json";
import koAssets from "./locales/ko/assets.json";
import koAuth from "./locales/ko/auth.json";
import koChat from "./locales/ko/chat.json";
import koChrome from "./locales/ko/chrome.json";
import koCommands from "./locales/ko/commands.json";
import koCommon from "./locales/ko/common.json";
import koCopyButton from "./locales/ko/copyButton.json";
import koDashboard from "./locales/ko/dashboard.json";
import koDateRangePicker from "./locales/ko/dateRangePicker.json";
import koExplorationGraph from "./locales/ko/explorationGraph.json";
import koFindingRetestDialog from "./locales/ko/findingRetestDialog.json";
import koFindingRetestPanel from "./locales/ko/findingRetestPanel.json";
import koFindings from "./locales/ko/findings.json";
import koFindingsComponents from "./locales/ko/findingsComponents.json";
import koFindingsDetail from "./locales/ko/findingsDetail.json";
import koFindingTrafficPanel from "./locales/ko/findingTrafficPanel.json";
import koHttpCodeBlock from "./locales/ko/httpCodeBlock.json";
import koIntercept from "./locales/ko/intercept.json";
import koLanguageToggle from "./locales/ko/languageToggle.json";
import koLinkTrafficDialog from "./locales/ko/linkTrafficDialog.json";
import koLlm from "./locales/ko/llm.json";
import koLlmRecords from "./locales/ko/llmRecords.json";
import koLogs from "./locales/ko/logs.json";
import koMcp from "./locales/ko/mcp.json";
import koMentionTextarea from "./locales/ko/mentionTextarea.json";
import koNav from "./locales/ko/nav.json";
import koNotFound from "./locales/ko/notFound.json";
import koNotify from "./locales/ko/notify.json";
import koScopeTextEditor from "./locales/ko/scopeTextEditor.json";
import koSettings from "./locales/ko/settings.json";
import koSideQuestionWorkspace from "./locales/ko/sideQuestionWorkspace.json";
import koSkills from "./locales/ko/skills.json";
import koStatus from "./locales/ko/status.json";
import koSync from "./locales/ko/sync.json";
import koTablePagination from "./locales/ko/tablePagination.json";
import koTaskAssets from "./locales/ko/taskAssets.json";
import koTaskLlmProfileChain from "./locales/ko/taskLlmProfileChain.json";
import koTasks from "./locales/ko/tasks.json";
import koTasksAssets from "./locales/ko/tasksAssets.json";
import koTasksBroadcast from "./locales/ko/tasksBroadcast.json";
import koTasksCoverage from "./locales/ko/tasksCoverage.json";
import koTasksDetail from "./locales/ko/tasksDetail.json";
import koTasksMisc from "./locales/ko/tasksMisc.json";
import koTasksOverview from "./locales/ko/tasksOverview.json";
import koTasksSessions from "./locales/ko/tasksSessions.json";
import koTaskTemplateControls from "./locales/ko/taskTemplateControls.json";
import koTodoPopover from "./locales/ko/todoPopover.json";
import koTools from "./locales/ko/tools.json";
import koTraffic from "./locales/ko/traffic.json";
import koTrafficEvidenceViewer from "./locales/ko/trafficEvidenceViewer.json";
import koTrafficPickerDialog from "./locales/ko/trafficPickerDialog.json";
import koTranscript from "./locales/ko/transcript.json";
import koWorkspace from "./locales/ko/workspace.json";

export const messages: Record<Locale, Record<string, unknown>> = {
  en: {
    common: enCommon,
    nav: enNav,
    auth: enAuth,
    chrome: enChrome,
    agents: enAgents,
    commands: enCommands,
    dashboard: enDashboard,
    chat: enChat,
    assets: enAssets,
    findings: enFindings,
    findingsComponents: enFindingsComponents,
    traffic: enTraffic,
    skills: enSkills,
    llmRecords: enLlmRecords,
    sync: enSync,
    workspace: enWorkspace,
    logs: enLogs,
    mcp: enMcp,
    findingsDetail: enFindingsDetail,
    intercept: enIntercept,
    llm: enLlm,
    notify: enNotify,
    settings: enSettings,
    tools: enTools,
    tasksAssets: enTasksAssets,
    tasksBroadcast: enTasksBroadcast,
    tasksCoverage: enTasksCoverage,
    tasksDetail: enTasksDetail,
    tasksMisc: enTasksMisc,
    tasksOverview: enTasksOverview,
    tasksSessions: enTasksSessions,
    taskTemplateControls: enTaskTemplateControls,
    taskAssets: enTaskAssets,
    transcript: enTranscript,
    explorationGraph: enExplorationGraph,
    agentEditor: enAgentEditor,
    approvalRecords: enApprovalRecords,
    assetDslSearch: enAssetDslSearch,
    findingTrafficPanel: enFindingTrafficPanel,
    mentionTextarea: enMentionTextarea,
    tasks: enTasks,
    trafficPickerDialog: enTrafficPickerDialog,
    httpCodeBlock: enHttpCodeBlock,
    trafficEvidenceViewer: enTrafficEvidenceViewer,
    findingRetestPanel: enFindingRetestPanel,
    taskLlmProfileChain: enTaskLlmProfileChain,
    approvalExecutionFocus: enApprovalExecutionFocus,
    linkTrafficDialog: enLinkTrafficDialog,
    sideQuestionWorkspace: enSideQuestionWorkspace,
    tablePagination: enTablePagination,
    dateRangePicker: enDateRangePicker,
    assetInterceptRulesEditor: enAssetInterceptRulesEditor,
    copyButton: enCopyButton,
    scopeTextEditor: enScopeTextEditor,
    todoPopover: enTodoPopover,
    findingRetestDialog: enFindingRetestDialog,
    notFound: enNotFound,
    languageToggle: enLanguageToggle,
    status: enStatus,
  },
  ko: {
    common: koCommon,
    nav: koNav,
    auth: koAuth,
    chrome: koChrome,
    agents: koAgents,
    commands: koCommands,
    dashboard: koDashboard,
    chat: koChat,
    assets: koAssets,
    findings: koFindings,
    findingsComponents: koFindingsComponents,
    traffic: koTraffic,
    skills: koSkills,
    llmRecords: koLlmRecords,
    sync: koSync,
    workspace: koWorkspace,
    logs: koLogs,
    mcp: koMcp,
    findingsDetail: koFindingsDetail,
    intercept: koIntercept,
    llm: koLlm,
    notify: koNotify,
    settings: koSettings,
    tools: koTools,
    tasksAssets: koTasksAssets,
    tasksBroadcast: koTasksBroadcast,
    tasksCoverage: koTasksCoverage,
    tasksDetail: koTasksDetail,
    tasksMisc: koTasksMisc,
    tasksOverview: koTasksOverview,
    tasksSessions: koTasksSessions,
    taskTemplateControls: koTaskTemplateControls,
    taskAssets: koTaskAssets,
    transcript: koTranscript,
    explorationGraph: koExplorationGraph,
    agentEditor: koAgentEditor,
    approvalRecords: koApprovalRecords,
    assetDslSearch: koAssetDslSearch,
    findingTrafficPanel: koFindingTrafficPanel,
    mentionTextarea: koMentionTextarea,
    tasks: koTasks,
    trafficPickerDialog: koTrafficPickerDialog,
    httpCodeBlock: koHttpCodeBlock,
    trafficEvidenceViewer: koTrafficEvidenceViewer,
    findingRetestPanel: koFindingRetestPanel,
    taskLlmProfileChain: koTaskLlmProfileChain,
    approvalExecutionFocus: koApprovalExecutionFocus,
    linkTrafficDialog: koLinkTrafficDialog,
    sideQuestionWorkspace: koSideQuestionWorkspace,
    tablePagination: koTablePagination,
    dateRangePicker: koDateRangePicker,
    assetInterceptRulesEditor: koAssetInterceptRulesEditor,
    copyButton: koCopyButton,
    scopeTextEditor: koScopeTextEditor,
    todoPopover: koTodoPopover,
    findingRetestDialog: koFindingRetestDialog,
    notFound: koNotFound,
    languageToggle: koLanguageToggle,
    status: koStatus,
  },
};
