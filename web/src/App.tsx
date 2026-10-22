import { createSignal, onCleanup, onMount } from "solid-js";
import ThreePaneShell from "./components/layout/ThreePaneShell";
import { hoxiAPI } from "./services/api";
import { connectEventStream } from "./services/sse";
import { watchNetworkStatus } from "./services/network";
import {
  advanceSessionGoal,
  applySessionDiffEvent,
  applyPartDeltaBatch,
  applyManagedStatusSnapshot,
  applyStreamFrameBatch,
  bootstrapProjectSessions,
  drainNextQueuedPrompt,
  handleIncomingPermissionRequest,
  markPermissionResolved,
  handleSessionErrorEvent,
  isDismissedProjectPath,
  isInvalidProjectPath,
  isManagedSession,
  isSessionBusy,
  indexSessionFromEvent,
  lastPromptDispatchedAt,
  loadAgents,
  loadApprovalPolicy,
  loadProjects,
  loadProviders,
  loadProviderTelemetry,
  loadSavedWorkspaces,
  loadSessionMessages,
  loadSessionMessagesOnly,
  normalizeFolderPath,
  recordSessionRetry,
  rememberQuestionReject,
  rememberQuestionReply,
  rememberQuestionRequest,
  removeSessionFromStore,
  replaceSessionFromEvent,
  resolveDefaultProjectDir,
  resolveParentSessionID,
  resyncResidentTranscripts,
  runSessionCodeAudit,
  scheduleRefreshWorktreeStatus,
  selectSession,
  setWorkspace,
  syncActiveProjectID,
  syncPendingPermissions,
  syncSessionStatuses,
  triggerDesktopNotification,
  trimSessionTranscripts,
  upsertMessageInfo,
  upsertMessagePart,
  workspace,
} from "./store/workspace";
import { t } from "./i18n";

export default function App() {
  const [online, setOnline] = createSignal(typeof navigator === "undefined" || navigator.onLine);
  const [eventStreamStarted, setEventStreamStarted] = createSignal(false);
  let retryEventStream: (() => void) | undefined;

  onMount(() => {
    let disconnectStream: (() => void) | undefined;
    const stopWatchingNetwork = watchNetworkStatus((isOnline) => {
      setOnline(isOnline);
      if (isOnline) retryEventStream?.();
    });
    const watchdogTimer = setInterval(() => {
      trimSessionTranscripts();
      // Managed sessions converge through the stream's global status snapshot,
      // so status polling is only needed for unadopted OpenCode sessions.
      const hasUnmanagedBusy = Object.entries(workspace.statusBySession).some(
        ([sid, s]) =>
          (s?.type === "busy" || s?.type === "retry") && !isManagedSession(sid)
      );
      const hasBusySession = Object.values(workspace.statusBySession).some(
        (s) => s?.type === "busy" || s?.type === "retry"
      );
      if (hasUnmanagedBusy) {
        void syncSessionStatuses();
      }
      // Approvals still need the REST fallback: an approval raised before the
      // stream connected (or while the user was elsewhere) is only visible there.
      if (hasBusySession || workspace.pendingPermissions.length > 0) {
        void syncPendingPermissions();
      }
    }, 4000);

    onCleanup(() => {
      clearInterval(watchdogTimer);
      disconnectStream?.();
      stopWatchingNetwork();
    });

    void (async () => {
      try {
        const boot = await hoxiAPI.getBootstrap();
        const bootRoot = isInvalidProjectPath(boot.rootDir)
          ? ""
          : normalizeFolderPath(boot.rootDir);
        setWorkspace({
          appVersion: boot.appVersion,
          rootDir: bootRoot,
          activeProjectID: null,
          wsBaseURL: boot.wsBaseURL,
        });

        const eagerBootRoot =
          bootRoot && !isDismissedProjectPath(bootRoot) ? bootRoot : "";
        const eagerSessionsPromise = eagerBootRoot
          ? bootstrapProjectSessions(eagerBootRoot).catch((err) => {
              console.warn("Initial session load retry pending:", err);
              return null;
            })
          : null;

        // Kick off non-blocking background loads while awaiting saved workspaces & projects
        void loadProviders();
        void loadProviderTelemetry();
        void loadAgents();
        void loadApprovalPolicy().catch((e) => {
          console.warn("Failed to load approval policy:", e);
        });
        void syncPendingPermissions(eagerBootRoot || undefined);
        void hoxiAPI
          .listQuestions()
          .then((qs) => {
            if (Array.isArray(qs)) setWorkspace("pendingQuestions", qs);
          })
          .catch(() => {});

        await Promise.all([loadSavedWorkspaces(), loadProjects()]);

        const activeRoot = resolveDefaultProjectDir(bootRoot);
        if (activeRoot && activeRoot !== workspace.rootDir) {
          setWorkspace("rootDir", activeRoot);
        }

        if (activeRoot) {
          const projectID = await syncActiveProjectID(activeRoot);
          if (eagerSessionsPromise && activeRoot === eagerBootRoot) {
            await eagerSessionsPromise;
            if (workspace.sessions.length === 0) {
              await bootstrapProjectSessions(activeRoot, true, projectID);
            }
          } else {
            await bootstrapProjectSessions(activeRoot, true, projectID);
          }
        } else {
          setWorkspace("bootstrappingSessions", false);
        }
      } catch (err) {
        setWorkspace("bootstrappingSessions", false);
        console.error("Bootstrap error:", err);
      }

      // Events may have been missed while disconnected; pull authoritative state.
      let resyncing = false;
      let resyncRequested = false;
      const resyncFromServer = async () => {
        resyncRequested = true;
        if (resyncing) return;
        resyncing = true;
        try {
          while (resyncRequested) {
            resyncRequested = false;
            await Promise.allSettled([
              syncSessionStatuses(), syncPendingPermissions(), resyncResidentTranscripts(),
              workspace.rootDir
                ? bootstrapProjectSessions(workspace.rootDir, false, workspace.activeProjectID)
                : Promise.resolve(),
              hoxiAPI.listQuestions().then((questions) => {
                if (Array.isArray(questions)) setWorkspace("pendingQuestions", questions);
              }),
            ]);
          }
        } finally { resyncing = false; }
      };
      const handlers: Parameters<typeof connectEventStream>[0] = {
        onConnectionChange: (connected) => {
          setWorkspace("connected", connected);
          if (connected) {
            void loadProviders();
            void loadProviderTelemetry();
            resyncFromServer();
            if (
              workspace.rootDir &&
              workspace.sessions.length === 0 &&
              !workspace.bootstrappingSessions
            ) {
              void bootstrapProjectSessions(workspace.rootDir).catch(() => {});
            }
          }
        },
        onResync: resyncFromServer,
        onDeltaBatch: applyPartDeltaBatch,
        onFrameBatch: applyStreamFrameBatch,
        isFocusedSession: (sid) =>
          !workspace.activeSessionID ||
          sid === workspace.activeSessionID ||
          resolveParentSessionID(sid) === workspace.activeSessionID,
        onEvent: (evt, directory) => {
          switch (evt.type) {
            case "message.updated":
              upsertMessageInfo(evt.properties.info);
              break;
            case "message.part.updated":
              upsertMessagePart(evt.properties.part);
              break;
            case "session.updated":
            case "session.created":
              indexSessionFromEvent(evt.properties.info, directory);
              replaceSessionFromEvent(evt.properties.info, directory);
              break;
            case "session.deleted": {
              const delID = evt.properties.sessionID || evt.properties.info?.id;
              if (delID) {
                const wasActive = workspace.activeSessionID === delID;
                const nextCandidate = removeSessionFromStore(delID);
                if (wasActive) {
                  if (nextCandidate) {
                    void selectSession(nextCandidate.id);
                  } else {
                    setWorkspace("activeSessionID", null);
                  }
                }
              }
              break;
            }
            case "session.status": {
              const sid = evt.properties.sessionID;
              const previousStatus = workspace.statusBySession[sid]?.type;
              const wasBusy = previousStatus === "busy" || previousStatus === "retry";
              setWorkspace("statusBySession", sid, evt.properties.status);
              setWorkspace("stalledSinceBySession", sid, 0);
              if (evt.properties.status.type === "busy") {
                setWorkspace("unreadCompletedBySession", sid, false);
              } else if (evt.properties.status.type === "idle") {
                setWorkspace("sessionFinishedAt", sid, Date.now());
                const parentID = resolveParentSessionID(sid);
                if (wasBusy && parentID) {
                  void loadSessionMessagesOnly(sid);
                  void loadSessionMessagesOnly(parentID);
                  scheduleRefreshWorktreeStatus(parentID, 600);
                } else if (wasBusy) {
                  void loadSessionMessages(sid).catch(console.error);
                }
                if (
                  wasBusy &&
                  !parentID &&
                  sid !== workspace.activeSessionID &&
                  !workspace.interruptedBySession[sid]
                ) {
                  setWorkspace("unreadCompletedBySession", sid, true);
                }
                void drainNextQueuedPrompt(sid);
                if (wasBusy && !parentID) void advanceSessionGoal(sid);
              } else if (evt.properties.status.type === "retry") {
                recordSessionRetry(
                  sid,
                  evt.properties.status.attempt,
                  evt.properties.status.message
                );
              }
              break;
            }
            case "session.idle": {
              const sid = evt.properties.sessionID;
              // Native runtimes send `session.status` idle right before this
              // event; that one may already have dispatched the next queued
              // prompt, whose busy state must not be flipped back to idle.
              const nextPromptRunning =
                isSessionBusy(sid) && Date.now() - lastPromptDispatchedAt(sid) <= 3500;
              if (!nextPromptRunning) {
                setWorkspace("statusBySession", sid, { type: "idle" });
                setWorkspace("stalledSinceBySession", sid, 0);
              }
              setWorkspace("sessionFinishedAt", sid, Date.now());
              const parentID = resolveParentSessionID(sid);
              if (parentID) {
                void loadSessionMessagesOnly(sid);
                void loadSessionMessagesOnly(parentID);
                scheduleRefreshWorktreeStatus(parentID, 600);
              } else {
                if (
                  sid !== workspace.activeSessionID &&
                  !workspace.interruptedBySession[sid]
                ) {
                  setWorkspace("unreadCompletedBySession", sid, true);
                }
                triggerDesktopNotification("Hoxi", t("timeline.turnCompleted"), { sessionID: sid, kind: "complete" });
                void loadSessionMessages(sid).then(() => {
                  void drainNextQueuedPrompt(sid);
                  void advanceSessionGoal(sid);
                  void runSessionCodeAudit(sid, false);
                });
              }
              break;
            }
            case "session.stall":
              setWorkspace(
                "stalledSinceBySession",
                evt.properties.sessionID,
                evt.properties.stalledSince || 0
              );
              if (evt.properties.stalledSince) {
                triggerDesktopNotification("Hoxi", t("timeline.stalledNotification"), { sessionID: evt.properties.sessionID, kind: "stall" });
              }
              break;
            case "session.diff":
              applySessionDiffEvent(evt.properties.sessionID, evt.properties.diff);
              if (evt.properties.sessionID === workspace.activeSessionID) {
                scheduleRefreshWorktreeStatus(evt.properties.sessionID, 600);
              }
              break;
            case "permission.asked":
              handleIncomingPermissionRequest(evt.properties);
              break;
            case "permission.replied":
              markPermissionResolved(evt.properties.requestID);
              break;
            case "question.asked":
            case "question.v2.asked":
              rememberQuestionRequest(evt.properties);
              setWorkspace("pendingQuestions", (prev) =>
                prev.some((q) => q.id === evt.properties.id) ? prev : [...prev, evt.properties]
              );
              triggerDesktopNotification("Hoxi", t("approval.questionTitle"), { sessionID: evt.properties.sessionID, kind: "attention" });
              break;
            case "question.replied":
            case "question.v2.replied":
              rememberQuestionReply(
                evt.properties.requestID,
                evt.properties.sessionID,
                evt.properties.answers
              );
              setWorkspace("pendingQuestions", (prev) =>
                prev.filter((q) => q.id !== evt.properties.requestID)
              );
              break;
            case "question.rejected":
            case "question.v2.rejected":
              rememberQuestionReject(evt.properties.requestID, evt.properties.sessionID);
              setWorkspace("pendingQuestions", (prev) =>
                prev.filter((q) => q.id !== evt.properties.requestID)
              );
              break;
            case "hoxi.status.snapshot":
              if (Array.isArray(evt.properties.busy)) {
                applyManagedStatusSnapshot(evt.properties.busy, evt.properties.stalled ?? {});
              }
              break;
            case "session.error":
              if (evt.properties.sessionID) {
                void handleSessionErrorEvent(evt.properties.sessionID, evt.properties.error as any);
              }
              console.error("session error:", evt.properties);
              break;
          }
        },
      };
      retryEventStream = () => {
        disconnectStream?.();
        disconnectStream = connectEventStream(handlers, undefined, workspace.wsBaseURL);
        setEventStreamStarted(true);
      };
      retryEventStream();
    })();
  });

  return (
    <ThreePaneShell
      network={{
        online,
        connected: () => workspace.connected,
        streamStarted: eventStreamStarted,
        onRetry: () => retryEventStream?.(),
      }}
    />
  );
}
