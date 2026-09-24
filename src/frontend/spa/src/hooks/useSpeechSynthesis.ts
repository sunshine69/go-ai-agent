/**
 * Hook that converts a streaming text stream into spoken audio using the
 * Web Speech API (SpeechSynthesis).
 *
 * The default browser TTS sounds terrible when you speak one word at a time
 * (chugging, stuttering, painfully slow). Instead, this hook buffers incoming
 * text and speaks larger chunks (sentences and clauses) as they complete. This
 * produces smooth, natural-sounding speech and keeps the output in sync with
 * the streaming text.
 */

import { useRef, useCallback, useEffect } from "react";

export interface UseSpeechSynthesisOptions {
  /** Called with the text that is about to be spoken. Useful for logging. */
  onSpeak?: (text: string) => void;
  /** Called when an error occurs during synthesis. */
  onError?: (error: string) => void;
}

interface UseSpeechSynthesisReturn {
  /** True while speech is being produced (queued or currently playing). */
  isSpeaking: boolean;
  /** Enqueue raw text. May be called incrementally with partial chunks. */
  enqueue: (text: string) => void;
  /** Flush any pending text so it gets spoken. */
  flush: () => void;
  /** Stop everything immediately and clear the queue. */
  stop: () => void;
}

// Regex to strip emojis, icons, and markdown asterisks before speaking.
const EMOJI_MD_RE = /[\u{1F600}-\u{1F64F}\u{1F300}-\u{1F5FF}\u{1F680}-\u{1F6FF}\u{2600}-\u{27BF}\u{1F900}-\u{1F9FF}\u{1F1E6}-\u{1F1FF}\u{1F191}-\u{1F251}\u{2B50}\u{2611}\u{263A}\u{231A}\u{23F0}\u{23F3}*]/gu;

// Boundary markers for splitting into speakable chunks: sentence punctuation
// (`.!?`) followed by whitespace, or commas/semicolons followed by whitespace.
const BOUNDARY_MARKER = /([.!?]\s+)|(,\s+)|(;\s+)/;

function useSpeechSynthesisInternal(
  onSpeak: ((text: string) => void) | undefined,
  onError: ((error: string) => void) | undefined,
) {
  const synthRef = useRef<SpeechSynthesis | null>(null);
  const voicesReady = useRef(false);
  const textBuffer = useRef<string>("");
  const utteranceQueue = useRef<SpeechSynthesisUtterance[]>([]);
  const isSpeaking = useRef(false);

  useEffect(() => {
    if (typeof window === "undefined" || !("speechSynthesis" in window)) {
      return;
    }
    const synth = window.speechSynthesis;
    synthRef.current = synth;

    const onVoicesChanged = () => {
      voicesReady.current = true;
    };
    if (synth.onvoiceschanged !== undefined) {
      synth.onvoiceschanged = onVoicesChanged;
    }
    // Some browsers fire this immediately if voices are already loaded.
    onVoicesChanged();

    return () => {
      try {
        synth.cancel();
      } catch {}
      synthRef.current = null;
      voicesReady.current = false;
    };
  }, []);

  const processQueue = useCallback(() => {
    const synth = synthRef.current;
    if (!synth) return;
    if (isSpeaking.current || utteranceQueue.current.length === 0) return;

    isSpeaking.current = true;
    const utterance = utteranceQueue.current.shift();
    if (!utterance) {
      isSpeaking.current = false;
      return;
    }

    if (onSpeak) {
      utterance.onstart = () => onSpeak(utterance.text);
    }
    utterance.onerror = (evt) => {
      isSpeaking.current = false;
      const msg =
        typeof evt.error === "string"
          ? evt.error
          : `speech_synthesis_error: ${evt.type}`;
      onError?.(msg);
      processQueue();
    };
    utterance.onend = () => {
      isSpeaking.current = false;
      processQueue();
    };

    try {
      synth.speak(utterance);
    } catch (err) {
      isSpeaking.current = false;
      onError?.(err instanceof Error ? err.message : String(err));
    }
  }, [onSpeak, onError]);

  const enqueue = useCallback((text: string) => {
    if (!text || !synthRef.current) return;

    // Strip emojis/icons/markdown that would otherwise get spoken as noise.
    const cleanToken = text.replace(EMOJI_MD_RE, "");
    textBuffer.current += cleanToken;

    // Check if we have a complete chunk (ends on a boundary marker).
    if (BOUNDARY_MARKER.test(textBuffer.current)) {
      // Find the last boundary and speak up to there.
      const match = textBuffer.current.match(/.*[.!?,;]\s+/);
      if (match) {
        const chunkToSpeak = match[0].trim();
        // Keep the remaining unpunctuated text in the buffer.
        textBuffer.current = textBuffer.current.substring(match[0].length);
        if (chunkToSpeak.length > 0) {
          queueText(chunkToSpeak);
        }
      }
    }
  }, []);

  const queueText = useCallback((chunk: string) => {
    if (!chunk || chunk.length < 2) return; // Ignore lone punctuation
    const utterance = new SpeechSynthesisUtterance(chunk);
    utterance.rate = 1.1; // Slightly faster for a snappier feel
    utteranceQueue.current.push(utterance);
    processQueue();
  }, [processQueue]);

  const flush = useCallback(() => {
    const buffer = textBuffer.current.trim();
    textBuffer.current = "";
    if (buffer.length > 0) {
      queueText(buffer);
    }
  }, [queueText]);

  const stop = useCallback(() => {
    try {
      synthRef.current?.cancel();
    } catch {}
    textBuffer.current = "";
    utteranceQueue.current = [];
    isSpeaking.current = false;
  }, []);

  return {
    isSpeaking: isSpeaking.current,
    enqueue,
    flush,
    stop,
  };
}

export function useSpeechSynthesis({
  onSpeak,
  onError,
}: UseSpeechSynthesisOptions = {}): UseSpeechSynthesisReturn {
  return useSpeechSynthesisInternal(onSpeak, onError);
}
