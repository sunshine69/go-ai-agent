/**
 * Hook that converts a streaming text stream into spoken audio using the
 * Web Speech API (SpeechSynthesis).
 *
 * Design decisions for reliability and natural-sounding output:
 *
 * 1. **Short utterances** — Split at the FIRST sentence boundary so each
 *    utterance is one sentence (< 180 chars). Chrome silently drops
 *    utterances that are too long.
 *
 * 2. **Natural pauses between sentences** — After each utterance ends we
 *    wait before speaking the next one: ~450 ms after a full stop (. ! ?),
 *    ~180 ms after a comma / semicolon. This gives the speech a natural
 *    breathing rhythm and prevents the engine from dropping words when
 *    utterances are fired back-to-back.
 *
 * 3. **Generation counter** — Every stop()/cancel() bumps a generation ID.
 *    Stale onend/onerror from a prior generation are discarded so they
 *    can't corrupt the queue (causes repeated words).
 *
 * 4. **15 s Chrome pause-bug heartbeat** — pause()/resume() every 12 s
 *    while an utterance is playing, so long sentences don't go silent.
 *
 * 5. **Rate 0.9** — Slightly slower than natural. TTS voices sound rushed
 *    at 1.0; 0.9 gives a relaxed, clear delivery.
 */

import { useRef, useCallback, useEffect, useState } from "react";

export interface UseSpeechSynthesisOptions {
  onSpeak?: (text: string) => void;
  onError?: (error: string) => void;
}

interface UseSpeechSynthesisReturn {
  isSpeaking: boolean;
  enqueue: (text: string) => void;
  flush: () => void;
  stop: () => void;
}

// Strip emojis, icons, markdown formatting before speaking.
const CLEAN_RE = new RegExp(
  "[\\u{1F600}-\\u{1F64F}\\u{1F300}-\\u{1F5FF}\\u{1F680}-\\u{1F6FF}" +
    "\\u{1F900}-\\u{1F9FF}\\u{1F1E6}-\\u{1F1FF}\\u{1F191}-\\u{1F251}" +
    "\\u{2B50}\\u{2611}\\u{263A}\\u{231A}\\u{23F0}\\u{23F3}" +
    "\\u{FE0F}\\u{200D}\\u{2190}-\\u{21FF}\\u{2700}-\\u{27BF}" +
    "[*~`#[\\]_]",
  "gu",
);

// Chrome drops utterances > ~200 chars silently.
const MAX_UTTERANCE_LEN = 180;

// Speaking rate — 0.9 is relaxed and clear. 1.0 sounds rushed for TTS.
const SPEAK_RATE = 0.9;

// Pause durations between utterances (ms).
const PAUSE_AFTER_SENTENCE = 450; // after . ! ?
const PAUSE_AFTER_CLAUSE = 180; // after , ; :

/**
 * Determine the pause length based on the last character of the chunk
 * that was just spoken.
 */
function pauseAfter(text: string): number {
  const last = text.trimEnd().slice(-1);
  if (last === "." || last === "!" || last === "?") return PAUSE_AFTER_SENTENCE;
  return PAUSE_AFTER_CLAUSE;
}

/**
 * Split text into a speakable chunk at the FIRST boundary.
 * Returns the chunk and the remaining text, or null if no boundary found.
 */
function splitFirstChunk(text: string): { chunk: string; rest: string } | null {
  // Sentence boundary: . ! ? followed by whitespace or end-of-string
  const sentMatch = text.match(/^(.*?[.!?])(\s+|$)(.*)$/s);
  if (sentMatch) {
    const chunk = sentMatch[1].trim();
    const rest = (sentMatch[2] + sentMatch[3]).trim();
    if (chunk.length >= 2) {
      if (chunk.length > MAX_UTTERANCE_LEN) return splitAtComma(chunk, rest);
      return { chunk, rest };
    }
  }

  // Clause boundary: , ; : followed by whitespace or end-of-string
  const commaMatch = text.match(/^(.*?[,;:])(\s+|$)(.*)$/s);
  if (commaMatch) {
    const chunk = commaMatch[1].trim();
    const rest = (commaMatch[2] + commaMatch[3]).trim();
    if (chunk.length >= 2) {
      if (chunk.length > MAX_UTTERANCE_LEN) return splitAtComma(chunk, rest);
      return { chunk, rest };
    }
  }

  return null;
}

/**
 * If a chunk is > MAX_UTTERANCE_LEN, split at the last comma/semicolon
 * within the limit.
 */
function splitAtComma(
  chunk: string,
  rest: string,
): { chunk: string; rest: string } {
  const limit = chunk.slice(0, MAX_UTTERANCE_LEN);
  const lastComma = Math.max(limit.lastIndexOf(","), limit.lastIndexOf(";"));
  if (lastComma > 20) {
    const head = chunk.slice(0, lastComma + 1).trim();
    const tail = chunk.slice(lastComma + 1).trim() + (rest ? " " + rest : "");
    return { chunk: head, rest: tail };
  }
  return { chunk, rest };
}

function useSpeechSynthesisInternal(
  onSpeak: ((text: string) => void) | undefined,
  onError: ((error: string) => void) | undefined,
) {
  const synthRef = useRef<SpeechSynthesis | null>(null);
  const textBuffer = useRef<string>("");
  const utteranceQueue = useRef<SpeechSynthesisUtterance[]>([]);
  const isSpeakingRef = useRef(false);
  const [isSpeaking, setIsSpeaking] = useState(false);

  // Generation counter — bumped on stop/cancel to invalidate stale callbacks.
  const generationRef = useRef(0);

  // Timer for the inter-sentence pause (so we can cancel it on stop()).
  const pauseTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // 15 s Chrome pause-bug heartbeat.
  const heartbeatRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const setSpeaking = useCallback((val: boolean) => {
    isSpeakingRef.current = val;
    setIsSpeaking(val);
  }, []);

  // --- 15 s pause-bug heartbeat: pause()/resume() while speaking ---
  const startHeartbeat = useCallback(() => {
    if (heartbeatRef.current) return;
    heartbeatRef.current = setInterval(() => {
      const synth = synthRef.current;
      if (!synth) return;
      try {
        if (synth.speaking && !synth.paused) {
          synth.pause();
          synth.resume();
        }
      } catch {
        // some browsers throw when idle
      }
    }, 12000);
  }, []);

  const stopHeartbeat = useCallback(() => {
    if (heartbeatRef.current) {
      clearInterval(heartbeatRef.current);
      heartbeatRef.current = null;
    }
  }, []);

  // Clear any pending inter-sentence pause timer.
  const clearPauseTimer = useCallback(() => {
    if (pauseTimerRef.current) {
      clearTimeout(pauseTimerRef.current);
      pauseTimerRef.current = null;
    }
  }, []);

  // --- Setup / teardown ---
  useEffect(() => {
    if (typeof window === "undefined" || !("speechSynthesis" in window)) return;
    const synth = window.speechSynthesis;
    synthRef.current = synth;
    synth.onvoiceschanged = () => {};
    synth.getVoices();

    return () => {
      stopHeartbeat();
      clearPauseTimer();
      generationRef.current++;
      try {
        synth.cancel();
      } catch {}
      synthRef.current = null;
    };
  }, [stopHeartbeat, clearPauseTimer]);

  // --- Speak the next utterance from the queue ---
  const speakNext = useCallback(() => {
    const synth = synthRef.current;
    if (!synth) return;
    if (utteranceQueue.current.length === 0) {
      // Queue is empty — done speaking.
      setSpeaking(false);
      stopHeartbeat();
      return;
    }

    const utterance = utteranceQueue.current.shift()!;
    const gen = generationRef.current;

    setSpeaking(true);
    startHeartbeat();

    utterance.onstart = () => {
      if (gen === generationRef.current && onSpeak) {
        onSpeak(utterance.text);
      }
    };

    utterance.onend = () => {
      if (gen !== generationRef.current) return;
      // Natural pause before the next sentence/clause.
      const delay = pauseAfter(utterance.text);
      pauseTimerRef.current = setTimeout(() => {
        pauseTimerRef.current = null;
        if (gen !== generationRef.current) return;
        speakNext();
      }, delay);
    };

    utterance.onerror = (evt) => {
      if (gen !== generationRef.current) return;
      // "canceled" / "interrupted" are expected during stop() — skip them.
      if (evt.error !== "canceled" && evt.error !== "interrupted") {
        onError?.(
          typeof evt.error === "string"
            ? evt.error
            : `speech_synthesis_error: ${evt.type}`,
        );
      }
      // On error, try the next utterance immediately (no pause).
      pauseTimerRef.current = setTimeout(() => {
        pauseTimerRef.current = null;
        if (gen === generationRef.current) speakNext();
      }, 50);
    };

    try {
      synth.speak(utterance);
    } catch (err) {
      setSpeaking(false);
      stopHeartbeat();
      onError?.(err instanceof Error ? err.message : String(err));
    }
  }, [onSpeak, onError, setSpeaking, startHeartbeat, stopHeartbeat]);

  // --- Queue a chunk and kick off speaking if idle ---
  const queueText = useCallback(
    (chunk: string) => {
      const text = chunk.trim();
      if (!text || text.length < 2) return;

      const utterance = new SpeechSynthesisUtterance(text);
      utterance.rate = SPEAK_RATE;
      utterance.pitch = 1.0;
      utteranceQueue.current.push(utterance);

      // Only start speaking if we're not already speaking or pausing.
      if (!isSpeakingRef.current && !pauseTimerRef.current) {
        speakNext();
      }
    },
    [speakNext],
  );

  // --- Enqueue streaming text: buffer, then speak at each boundary ---
  const enqueue = useCallback(
    (text: string) => {
      if (!text || !synthRef.current) return;

      const cleanToken = text.replace(CLEAN_RE, "");
      textBuffer.current += cleanToken;

      let result = splitFirstChunk(textBuffer.current);
      while (result) {
        const { chunk, rest } = result;
        textBuffer.current = rest;
        if (chunk.length > 0) queueText(chunk);
        result = splitFirstChunk(rest);
      }
    },
    [queueText],
  );

  // --- Flush: speak any remaining buffered text ---
  const flush = useCallback(() => {
    const buffer = textBuffer.current.trim();
    textBuffer.current = "";
    if (buffer.length > 0) {
      if (buffer.length > MAX_UTTERANCE_LEN) {
        const { chunk, rest } = splitAtComma(buffer, "");
        queueText(chunk);
        if (rest) queueText(rest);
      } else {
        queueText(buffer);
      }
    }
  }, [queueText]);

  // --- Stop: cancel everything ---
  const stop = useCallback(() => {
    generationRef.current++;
    stopHeartbeat();
    clearPauseTimer();
    try {
      synthRef.current?.cancel();
    } catch {}
    textBuffer.current = "";
    utteranceQueue.current = [];
    setSpeaking(false);
  }, [setSpeaking, stopHeartbeat, clearPauseTimer]);

  return { isSpeaking, enqueue, flush, stop };
}

export function useSpeechSynthesis({
  onSpeak,
  onError,
}: UseSpeechSynthesisOptions = {}): UseSpeechSynthesisReturn {
  return useSpeechSynthesisInternal(onSpeak, onError);
}
