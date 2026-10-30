/*
 * Message composer (UI-07). Enter sends the trimmed text and Shift+Enter adds a line, with a mouse
 * or a trackpad; on a touch device Enter adds a line and only the Send button sends (ENT-01..ENT-05,
 * shared/lib/device.ts).. Text over MaxTextLength (MSG-02) cannot be sent; files alone can. A post is a message to the room's
 * intelligences: chats are not members and there is no recipient picker any more (CID-22,
 * ROUTE-06/DLV-03..05 and their listen-only mapping are retired). The body carries the text and
 * files only. `members` still feeds the mention list below. Files come from the paperclip, a paste
 * or a drop, all through the same list; the attachment row previews them (images as thumbnails,
 * other kinds as an icon with name and size) and scrolls sideways instead of growing the composer.
 *
 * A microphone button records a voice message (VOI-01..VOI-06, VOI-09, features/send-voice-message): a tap
 * records until Send or Cancel, a hold records while held. The browser hears it as it happens, so
 * the message carries the transcript as its text (a caption already in the box comes first, on its
 * own line); without a transcript it is an audio attachment with no text, as before, and the draft
 * stays where it is. The recording bar takes the text
 * box's place, so focus moves to the button that sends it and, when the recording ends, goes back
 * to the text box if it was there and stays on the microphone otherwise (no keyboard pops up
 * after a hold).
 *
 * A message chosen to answer (ACT-07) shows as a strip above the text box, with a way to take it
 * back; the next message sent, text or voice, carries its id as `reply_to`.
 *
 * The same box edits a message of the viewer's (EDT-10): given `editing` it loads that message's text under an
 * "Editing message" bar, Enter (or Save) saves it and Escape (or Cancel) leaves, the attach button is gone (the
 * files of a message stay, only its text changes), and what the box held before comes back afterwards.
 *
 * Typing `@` at the start of a word suggests the conversation's participants (MEN-01..MEN-06):
 * the filtered list opens above the box, Up/Down move, Enter, Tab or a tap complete `@handle `
 * and never send, Escape or leaving the `@word` closes it, and Enter sends as usual when nothing
 * matches.
 */

import { CheckIcon, PaperclipIcon, SendHorizontalIcon, Trash2Icon } from "lucide-react";
import {
  type ChangeEvent,
  type ClipboardEvent,
  type DragEvent,
  type KeyboardEvent,
  type SyntheticEvent,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import { useMe } from "@/entities/account";
import { messageExcerpt, useEditMessage, usePostMessage } from "@/entities/message";
import { EditBar } from "@/features/edit-message";
import {
  type MentionCandidate,
  MentionSuggestions,
  matchMentions,
  mentionAt,
  mentionCandidates,
  mentionOptionId,
} from "@/features/mention-autocomplete";
import {
  carriesFiles,
  keepFocus,
  ReplyStrip,
  useDraft,
  useDraftStore,
  useEditDraft,
} from "@/features/send-message";
import {
  type HoldState,
  MicButton,
  RecordingBar,
  useLiveTranscript,
  useVoiceRecorder,
  type VoiceFailure,
} from "@/features/send-voice-message";
import { errorText } from "@/shared/api/error-text";
import { MaxTextLength, type Message, type RoomMember } from "@/shared/api/types.gen";
import { type MessageKey, useI18n } from "@/shared/i18n";
import { useEnterSends } from "@/shared/lib/device";
import { cn } from "@/shared/lib/utils";
import { textLength } from "@/shared/lib/validation";
import { AttachmentPreviews } from "@/shared/ui/molecules/attachment-preview";
import { Button } from "@/shared/ui/primitives/button";

/** The counter appears once the text gets this close to the limit. */
const COUNTER_FROM = MaxTextLength - 1_000;

/** What each reason the microphone could not be had is called (VOI-06). */
const MIC_FAILURES: Record<VoiceFailure, MessageKey> = {
  denied: "composer.micDenied",
  missing: "composer.micMissing",
  busy: "composer.micBusy",
  failed: "composer.micFailed",
};

export function ComposerForm({
  roomId,
  roomName,
  members,
  replyTo,
  onClearReply,
  editing,
  onEditDone,
}: {
  roomId: string;
  roomName: string;
  members?: RoomMember[];
  replyTo: Message | null;
  onClearReply?: () => void;
  editing: Message | null;
  onEditDone?: () => void;
}) {
  const { t, locale } = useI18n();
  // The voice recording being sent: its post only takes the reply back, never the typed draft.
  const voiceFile = useRef<File | null>(null);
  // A post that went through clears this conversation's draft, even when the person has already
  // moved to another conversation (the composer is gone, the callbacks of `mutate` are not run).
  // A voice message is one by its file, with or without the transcript as text: it takes the reply
  // back but leaves the typed draft (and its files) for the message the person was writing.
  const post = usePostMessage(roomId, (input) => {
    const store = useDraftStore.getState();
    const isVoice = input.files?.length === 1 && input.files[0] === voiceFile.current;
    if (isVoice) store.setReply(roomId, null);
    else store.clear(roomId);
  });
  const save = useEditMessage(roomId);
  // The draft lives in the store (WA-07), so it survives leaving the conversation; an edit has its
  // own, per message, and never touches the one of the new message.
  const draftStore = useDraftStore.getState();
  const draft = useDraft(roomId);
  const editId = editing?.id ?? null;
  const editDraft = useEditDraft(editId);
  const text = editing ? (editDraft ?? editing.text) : draft.text;
  const setText = (value: string) =>
    editing ? draftStore.setEditDraft(editing.id, value) : draftStore.setText(roomId, value);
  const attached = draft.files;
  const files = useMemo(() => attached.map((a) => a.file), [attached]);
  // A file is being dragged over the composer: it says so and takes no drop until it leaves.
  const [dragging, setDragging] = useState(false);
  const dragDepth = useRef(0);
  const [error, setError] = useState<string | null>(null);
  // The caret is part of the mention state: the list follows the `@word` it is in (MEN-04).
  const [caret, setCaret] = useState(0);
  const [activeIndex, setActiveIndex] = useState(0);
  // The `@word` the user closed with Escape; leaving it reopens the list on the next one.
  const [dismissedStart, setDismissedStart] = useState<number | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const textRef = useRef<HTMLTextAreaElement>(null);
  // Where the caret goes once a completion re-renders the controlled textarea.
  const pendingCaret = useRef<number | null>(null);
  const counterId = useId();
  const replyStripId = useId();
  const errorId = useId();
  const barId = useId();
  const mentionListId = useId();
  const viewerId = useMe().data?.id;
  // False on a touch device, where Enter is a new line (ENT-01).
  const sendsOnEnter = useEnterSends();
  const voice = useVoiceRecorder();
  // The browser hears the recording as it happens (VOI-09): its final text goes with the audio.
  const transcript = useLiveTranscript(voice.phase === "recording");
  // A finger that is down on the microphone: a hold, and whether it has slid far enough to cancel.
  const [hold, setHold] = useState<HoldState>("off");
  const [announcement, setAnnouncement] = useState("");
  // From the moment Send stops a recording until the answer: that stop is the user's own.
  const sendingVoice = useRef(false);
  // Each recording that starts moves it on: an upload's late answer only clears the recording it
  // was sent from, never one that started while the upload was in flight (VOI-04).
  const voiceRun = useRef(0);
  const micRef = useRef<HTMLButtonElement>(null);
  // Whether the text box had focus when the recording began. It gets focus back afterwards, and
  // only then: a hold on the microphone must not open the on-screen keyboard (VOI-04).
  const typing = useRef(false);
  // What takes focus once the recording is over and its bar has gone.
  const refocus = useRef<"text" | "mic" | null>(null);
  const voiceOn = voice.phase === "recording" || voice.phase === "recorded";

  const replyExcerpt = replyTo ? messageExcerpt(replyTo, t("player.voice")) : "";
  const shownError = voice.failure ? t(MIC_FAILURES[voice.failure]) : error;

  // Editing a message (EDT-10): the box shows that message's text (or what was typed in it before),
  // and the new-message draft comes back when editing ends. This follows the message by its id, so a
  // refreshed copy of the same message does not reset what is being typed.
  const [shownEdit, setShownEdit] = useState<string | null>(null);
  if (editId !== shownEdit) {
    setShownEdit(editId);
    setCaret(text.length);
    setActiveIndex(0);
    setDismissedStart(null);
    setError(null);
  }
  useEffect(() => {
    // Entering (or switching) puts the caret at the end of the text, ready to type. One tick later: the
    // menu that was chosen from is still on screen for a moment, and its focus trap would take the
    // cursor straight back.
    if (editId === null) return;
    const timer = window.setTimeout(() => {
      const box = textRef.current;
      if (!box) return;
      box.focus();
      box.setSelectionRange(box.value.length, box.value.length);
    }, 0);
    return () => window.clearTimeout(timer);
  }, [editId]);

  const length = textLength(text);
  const tooLong = length > MaxTextLength;
  const trimmed = text.trim();
  const canSend = !tooLong && (trimmed.length > 0 || files.length > 0) && !post.isPending;
  // Saving needs a change to the trimmed text (surrounding whitespace alone is no change), and text
  // unless the message has files that stay (EDT-04).
  const canSave =
    editing !== null &&
    !tooLong &&
    trimmed !== editing.text.trim() &&
    (trimmed.length > 0 || editing.attachments.length > 0) &&
    !save.isPending;
  const fmt = (n: number) => new Intl.NumberFormat(locale).format(n);

  // The mention list follows the caret: it is open while the caret sits in an `@word` that has
  // matching participants and that the user did not just close (MEN-01, MEN-04, MEN-05).
  const mention = mentionAt(text, caret);
  const query = mention?.query ?? null;
  const mentionCandidatesShown = useMemo(
    () => mentionCandidates(members ?? [], viewerId),
    [members, viewerId],
  );
  const candidates = useMemo(
    () => (query === null ? [] : matchMentions(mentionCandidatesShown, query)),
    [mentionCandidatesShown, query],
  );
  const open = mention !== null && candidates.length > 0 && mention.start !== dismissedStart;
  // A shorter match set keeps the highlight inside it.
  const active = candidates.length > 0 ? Math.min(activeIndex, candidates.length - 1) : 0;
  const activeCandidate = candidates[active];
  const mentionStart = mention?.start ?? null;

  // Choosing a message to answer puts the cursor in the box (and opens the keyboard of a phone).
  // One tick later: the menu that was chosen from is still on screen for a moment, and its focus
  // trap would take the cursor straight back.
  const replyId = replyTo?.id;
  useEffect(() => {
    if (replyId === undefined) return;
    const timer = window.setTimeout(() => textRef.current?.focus(), 0);
    return () => window.clearTimeout(timer);
  }, [replyId]);

  // Leaving the `@word` (or sending) resets the Escape dismissal; the next mention opens again.
  useEffect(() => {
    if (mentionStart === null) setDismissedStart(null);
  }, [mentionStart]);

  // A completion rewrites the text; put the caret after the inserted `@handle `.
  useEffect(() => {
    const position = pendingCaret.current;
    if (position === null || textRef.current?.value !== text) return;
    pendingCaret.current = null;
    const box = textRef.current;
    if (!box) return;
    box.focus();
    box.setSelectionRange(position, position);
    setCaret(position);
  }, [text]);

  // What a screen reader hears of a recording: that it started, and that it stopped by itself.
  useEffect(() => {
    if (voice.phase === "recording") setAnnouncement(t("composer.announceRecording"));
    else if (voice.phase === "recorded" && !sendingVoice.current) {
      setAnnouncement(t("composer.announceReady"));
    }
  }, [voice.phase, t]);

  // The text box is hidden for as long as a recording is on, and focus must not fall off the page
  // with it: it moves to the button that sends the recording, which Escape and a screen reader
  // then still reach (VOI-02, VOI-05).
  useEffect(() => {
    if (voice.phase !== "recording") return;
    const active = document.activeElement;
    if (!active || active === document.body || active === textRef.current) {
      micRef.current?.focus({ preventScroll: true });
    }
  }, [voice.phase]);

  // The bar is gone again: focus goes back to where it was before the recording.
  useEffect(() => {
    if (voice.phase !== "idle" || !refocus.current) return;
    const target = refocus.current === "text" ? textRef : micRef;
    refocus.current = null;
    target.current?.focus({ preventScroll: true });
  }, [voice.phase]);

  const startVoice = () => {
    voiceRun.current += 1;
    transcript.reset();
    typing.current = document.activeElement === textRef.current;
    refocus.current = null;
    void voice.start();
  };

  /**
   * Stops the recording and posts it as a message of its own: one audio file, and the text the
   * browser heard as its transcript (VOI-04, VOI-09). Text already in the box is the caption and
   * comes first, on its own line; it is sent with the recording and the draft keeps only the rest.
   * Without a transcript the audio goes alone, as before, and the draft is untouched.
   */
  const sendVoice = async () => {
    if (post.isPending) return;
    sendingVoice.current = true;
    const recorded = await voice.stop();
    if (!recorded) {
      sendingVoice.current = false;
      return;
    }
    const spoken = await transcript.finish();
    const typed = editing ? "" : draft.text.trim();
    const messageText = spoken ? [typed, spoken].filter(Boolean).join("\n") : "";
    const run = voiceRun.current;
    voiceFile.current = recorded.file;
    setError(null);
    post.mutate(
      {
        text: messageText,
        files: [recorded.file],
        ...(replyTo ? { reply_to: replyTo.id } : {}),
      },
      {
        onSuccess: () => {
          sendingVoice.current = false;
          // The text that went with the recording is not left behind in the draft.
          if (messageText) draftStore.setText(roomId, "");
          refocus.current = typing.current ? "text" : "mic";
          setAnnouncement("");
          if (replyTo) onClearReply?.();
          // Only the recording that was sent: a newer one that started meanwhile stays where it is.
          if (voiceRun.current === run) voice.cancel();
        },
        // The recording stays, stopped, to send again or to discard.
        onError: (err) => {
          sendingVoice.current = false;
          setError(errorText(t, err, "post"));
        },
      },
    );
  };

  const cancelVoice = () => {
    transcript.reset();
    refocus.current = typing.current ? "text" : "mic";
    voice.cancel();
    setHold("off");
    setError(null);
    setAnnouncement(t("composer.announceDiscarded"));
  };

  const send = () => {
    if (!canSend) return;
    voice.clearFailure();
    setError(null);
    post.mutate(
      {
        text: trimmed,
        ...(files.length ? { files } : {}),
        ...(replyTo ? { reply_to: replyTo.id } : {}),
      },
      {
        onSuccess: () => {
          draftStore.clear(roomId);
          if (replyTo) onClearReply?.();
          textRef.current?.focus();
        },
        onError: (err) => setError(errorText(t, err, "post")),
      },
    );
  };

  /** Leaves editing, dropping what was typed in the edit box. */
  const leaveEdit = () => {
    if (editing) draftStore.clearEditDraft(editing.id);
    onEditDone?.();
  };

  const saveEdit = () => {
    if (!editing || !canSave) return;
    setError(null);
    save.mutate(
      { id: editing.id, text: trimmed },
      {
        onSuccess: () => {
          leaveEdit();
          textRef.current?.focus();
        },
        onError: (err) => setError(errorText(t, err, "edit")),
      },
    );
  };
  const submit = editing ? saveEdit : send;

  // Replaces the `@word` with `@handle:label ` (MEN-03), or `@handle ` for the AI itself; it
  // never sends the message itself.
  const completeMention = (candidate: MentionCandidate) => {
    if (!mention) return;
    const address = candidate.label
      ? `@${candidate.intelligence.handle}:${candidate.label}`
      : `@${candidate.intelligence.handle}`;
    const inserted = `${address} `;
    const position = mention.start + inserted.length;
    pendingCaret.current = position;
    setText(`${text.slice(0, mention.start)}${inserted}${text.slice(mention.end)}`);
    setCaret(position);
    setActiveIndex(0);
    setDismissedStart(null);
  };

  // The caret is read from every event that can move it, so the list closes as soon as it leaves
  // the `@word` (MEN-04); `select` alone does not cover every browser's arrow keys.
  const syncCaret = (e: SyntheticEvent<HTMLTextAreaElement>) =>
    setCaret(e.currentTarget.selectionStart ?? 0);

  const onChange = (e: ChangeEvent<HTMLTextAreaElement>) => {
    setText(e.target.value);
    setCaret(e.target.selectionStart ?? e.target.value.length);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.nativeEvent.isComposing) return;
    if (open && mention) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const step = e.key === "ArrowDown" ? 1 : -1;
        setActiveIndex(
          (current) =>
            (Math.min(current, candidates.length - 1) + step + candidates.length) %
            candidates.length,
        );
        return;
      }
      if (e.key === "Escape") {
        // MEN-04: close it, keeping the text untouched.
        e.preventDefault();
        setDismissedStart(mention.start);
        return;
      }
      if ((e.key === "Enter" && !e.shiftKey) || (e.key === "Tab" && !e.shiftKey)) {
        // MEN-03: the first candidate is active unless the user moved with Up/Down.
        if (!activeCandidate) return;
        e.preventDefault();
        completeMention(activeCandidate);
        return;
      }
    }
    if (e.key === "Escape" && replyTo && onClearReply) {
      // Take the reply back, keeping what was typed.
      e.preventDefault();
      onClearReply();
      return;
    }
    if (e.key !== "Enter" || e.shiftKey || !sendsOnEnter) return;
    e.preventDefault();
    submit();
  };

  const addFiles = (picked: readonly File[]) => {
    if (!picked.length) return;
    draftStore.addFiles(roomId, picked);
    setAnnouncement(
      picked.length === 1
        ? t("composer.announceFileAttached")
        : t("composer.announceFilesAttached", { count: picked.length }),
    );
  };

  // The paperclip: the chosen files join the ones already there, and the input can pick them again.
  const pickFiles = (list: FileList | null) => {
    if (!list?.length) return;
    addFiles(Array.from(list));
    if (fileInput.current) fileInput.current.value = "";
  };

  /* Pasting a screenshot or a file attaches it exactly as the paperclip would; a paste with only
     text is left to the browser, so writing carries on as before. When an app puts both an image
     and its link on the clipboard, the file wins and the link is not typed into the box. */
  const onPaste = (e: ClipboardEvent<HTMLTextAreaElement>) => {
    if (editing || voiceOn) return;
    const pasted = Array.from(e.clipboardData?.files ?? []);
    if (!pasted.length) return;
    e.preventDefault();
    addFiles(pasted);
  };

  const onDragEnter = (e: DragEvent<HTMLFormElement>) => {
    if (!carriesFiles(e.dataTransfer)) return;
    e.preventDefault();
    if (editing || voiceOn) return;
    dragDepth.current += 1;
    setDragging(true);
  };
  // A file dropped on the composer is never left to the browser, which would open it and leave
  // the app: while editing or recording it is swallowed without being added.
  const onDragOver = (e: DragEvent<HTMLFormElement>) => {
    if (!carriesFiles(e.dataTransfer)) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = editing || voiceOn ? "none" : "copy";
  };
  const onDragLeave = () => {
    dragDepth.current = Math.max(0, dragDepth.current - 1);
    if (dragDepth.current === 0) setDragging(false);
  };
  const onDrop = (e: DragEvent<HTMLFormElement>) => {
    if (!carriesFiles(e.dataTransfer)) return;
    e.preventDefault();
    dragDepth.current = 0;
    setDragging(false);
    if (editing || voiceOn) return;
    addFiles(Array.from(e.dataTransfer.files));
    textRef.current?.focus();
  };

  const describedBy = [
    editing ? barId : null,
    replyTo && !editing ? replyStripId : null,
    length >= COUNTER_FROM ? counterId : null,
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <form
      className="border-t bg-background px-3 pt-2.5 pb-3 md:px-6"
      data-testid="composer-dropzone"
      data-dragging={dragging ? "true" : undefined}
      onDragEnter={onDragEnter}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
      onKeyDown={(e) => {
        // Escape leaves editing from anywhere in the composer, the Cancel button included. A mention list
        // that is open closes first (it prevents the default of its Escape).
        if (editing && e.key === "Escape" && !e.defaultPrevented) {
          e.preventDefault();
          leaveEdit();
          return;
        }
        // Escape drops a recording, wherever focus is in the composer (VOI-05).
        if (e.key === "Escape" && voiceOn) {
          e.preventDefault();
          cancelVoice();
        }
      }}
    >
      {shownError ? (
        <p id={errorId} role="alert" className="mb-2 text-sm text-destructive">
          {shownError}
        </p>
      ) : null}
      <p role="status" className="sr-only">
        {announcement}
      </p>
      <div
        className={cn(
          "relative rounded-lg border border-input bg-card shadow-xs transition-[border-color,box-shadow] focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/25",
          tooLong &&
            "border-destructive focus-within:border-destructive focus-within:ring-destructive/20",
        )}
      >
        {open && !voiceOn ? (
          <MentionSuggestions
            id={mentionListId}
            candidates={candidates}
            activeIndex={active}
            onPick={completeMention}
          />
        ) : null}
        {replyTo && !editing ? (
          <ReplyStrip
            id={replyStripId}
            replyTo={replyTo}
            excerpt={replyExcerpt}
            onClear={onClearReply}
          />
        ) : null}
        {voiceOn ? (
          <RecordingBar
            phase={voice.phase as "recording" | "recorded"}
            elapsedMs={voice.elapsedMs}
            hold={hold}
            transcript={voice.phase === "recording" ? transcript.text : transcript.final}
            unavailable={transcript.unavailable}
          />
        ) : null}
        {editing ? <EditBar id={barId} message={editing} onLeave={leaveEdit} /> : null}
        {!editing && files.length > 0 && !voiceOn ? (
          <AttachmentPreviews
            files={attached}
            onRemove={(id) => draftStore.removeFile(roomId, id)}
          />
        ) : null}
        {/* biome-ignore lint/a11y/useAriaPropsSupportedByRole: while the mention list is open the box has role combobox */}
        <textarea
          ref={textRef}
          value={text}
          onChange={onChange}
          onKeyDown={onKeyDown}
          onSelect={syncCaret}
          onKeyUp={syncCaret}
          onClick={syncCaret}
          onFocus={syncCaret}
          onPaste={onPaste}
          rows={1}
          hidden={voiceOn}
          aria-label={t("composer.label", { room: roomName })}
          aria-invalid={tooLong || undefined}
          /* MEN-06: with the list open the box is the popup's combobox and names the active
             option; closed it stays the plain textarea the rest of the app looks for. */
          role={open ? "combobox" : undefined}
          aria-expanded={open ? true : undefined}
          aria-controls={open ? mentionListId : undefined}
          aria-autocomplete={open ? "list" : undefined}
          aria-haspopup={open ? "listbox" : undefined}
          aria-activedescendant={
            open && activeCandidate ? mentionOptionId(mentionListId, activeCandidate) : undefined
          }
          aria-describedby={describedBy || undefined}
          // The on-screen Return key shows what it does (ENT-04).
          enterKeyHint={sendsOnEnter ? (editing ? "done" : "send") : "enter"}
          placeholder={t("composer.placeholder", { room: roomName })}
          className={cn(
            "block max-h-[min(10rem,calc(var(--app-height,100dvh)*0.4))] min-h-11 w-full resize-none overflow-y-auto bg-transparent px-3 pt-2.5 pb-1 text-[0.9375rem] leading-relaxed outline-none [field-sizing:content] placeholder:text-muted-foreground",
            voiceOn && "hidden",
          )}
        />
        <div className="flex items-center gap-1 px-1.5 pb-1.5">
          {voiceOn ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="text-muted-foreground pointer-coarse:h-11"
              aria-label={t(
                voice.phase === "recorded"
                  ? "composer.discardRecording"
                  : "composer.cancelRecording",
              )}
              onClick={cancelVoice}
            >
              <Trash2Icon aria-hidden="true" />
              {t(voice.phase === "recorded" ? "composer.discard" : "common.cancel")}
            </Button>
          ) : editing ? null : (
            /* The file input itself takes focus; the label draws it as an icon button. A message that
               is edited keeps the files it has and takes no others (EDT-10). */
            <label
              title={t("composer.attach")}
              className="relative inline-flex size-8 cursor-pointer items-center justify-center rounded-md text-muted-foreground transition-colors focus-within:ring-[3px] focus-within:ring-ring/70 hover:bg-accent hover:text-accent-foreground pointer-coarse:size-11"
            >
              <input
                ref={fileInput}
                type="file"
                multiple
                className="sr-only"
                aria-label={t("composer.attach")}
                onChange={(e) => pickFiles(e.target.files)}
              />
              <PaperclipIcon className="size-4" aria-hidden="true" />
            </label>
          )}
          {/* A phone has no Shift key to remember, but it does need to be told Enter no longer
              sends: its hint shows at every width and gives way to the counter's room (ENT-03). */}
          {voiceOn ? null : (
            <span
              className={cn(
                "min-w-0 flex-1 text-balance text-right text-[0.7rem] leading-tight text-muted-foreground",
                // Two lines keep the whole hint beside the three buttons of a 360 px phone.
                sendsOnEnter ? "hidden truncate lg:block" : "line-clamp-2",
              )}
            >
              {t(
                editing
                  ? sendsOnEnter
                    ? "composer.editHint"
                    : "composer.editHintTouch"
                  : sendsOnEnter
                    ? "composer.hint"
                    : "composer.hintTouch",
              )}
            </span>
          )}
          <div className="ml-auto flex items-center gap-1">
            {!voiceOn && length >= COUNTER_FROM ? (
              <span
                id={counterId}
                data-testid="composer-counter"
                className={cn(
                  "mr-1 shrink-0 font-mono text-[0.7rem] tabular",
                  tooLong ? "font-semibold text-destructive" : "text-muted-foreground",
                )}
              >
                {fmt(length)} / {fmt(MaxTextLength)}
              </span>
            ) : null}
            {voice.supported && !editing ? (
              <MicButton
                ref={micRef}
                phase={voice.phase}
                sending={voiceOn && post.isPending}
                onStart={startVoice}
                onSend={() => void sendVoice()}
                onCancel={cancelVoice}
                onHold={setHold}
                className="pointer-coarse:size-11"
              />
            ) : null}
            {voiceOn ? null : (
              <Button
                type="submit"
                size="icon-sm"
                disabled={editing ? !canSave : !canSend}
                aria-label={t(editing ? "composer.saveEdit" : "composer.send")}
                // A press on Send must not take focus from the text box: that closes the on-screen
                // keyboard, and the composer jumps while it does (KBD-03). The click still sends.
                onPointerDown={keepFocus}
                onMouseDown={keepFocus}
                className="pointer-coarse:size-11"
              >
                {editing ? (
                  <CheckIcon aria-hidden="true" />
                ) : (
                  <SendHorizontalIcon aria-hidden="true" />
                )}
              </Button>
            )}
          </div>
        </div>
        {dragging ? (
          <div
            data-testid="composer-drop-hint"
            className="pointer-events-none absolute inset-0 flex items-center justify-center rounded-lg border-2 border-dashed border-ring bg-background/85 text-sm font-medium text-ring"
          >
            {t("composer.dropFiles")}
          </div>
        ) : null}
      </div>
    </form>
  );
}
