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
 * 4. **No pause/resume heartbeat** — We deliberately do NOT call
 *    synth.pause()/synth.resume() while speaking. On remote cloud voices
 *    (e.g. "Google US English" streams from Google's servers) an instant
 *    back-to-back pause()/resume() races the audio buffer and skips words.
 *    Moreover, because every utterance is < 180 chars, no single utterance
 *    can ever last long enough to warrant it, so it was pure dead weight.
 *
 * 5. **Pin utterance.lang + voice** — Every utterance is explicitly bound to
 *    'en-US' and, when available, the same voice object returned by
 *    getVoices(). Without this Chrome may dynamically fall back to a
 *    network/remote voice that stalls on odd characters and swallows the
 *    remaining text payload.
 *
 * 6. **Never queue an oversized utterance** — `enqueue()` self-corrects
 *    because its while-loop keeps re-splitting the shrinking remainder
 *    until every piece is <= MAX_UTTERANCE_LEN. `flush()` only sees text
 *    once, at stream end, so it must split RECURSIVELY too (see
 *    `splitIntoChunks`) — a single split can still leave a >180 char tail
 *    (e.g. a long, comma-sparse trailing sentence with no terminal
 *    punctuation), which Chrome then drops silently. `queueText()` also
 *    carries the same length guard as a last-line defense so no caller,
 *    present or future, can push an utterance past the drop threshold.
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
const SPEAK_RATE = 1.0;

// Voice pinning for remote cloud voices (e.g. Google US English).
const VOICE_LANG = "en-US";

// Pause durations between utterances (ms).
//const PAUSE_AFTER_SENTENCE = 450; // after . ! ?
const PAUSE_AFTER_SENTENCE = 100; // after . ! ?
//const PAUSE_AFTER_CLAUSE = 180; // after , ; :
const PAUSE_AFTER_CLAUSE = 50; // after , ; :

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
 * within the limit. Performs a SINGLE cut — callers that need the whole
 * string reduced to <= MAX_UTTERANCE_LEN pieces must loop (see
 * `splitIntoChunks`), since the returned `rest` is not itself guaranteed
 * to be under the limit.
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
  // No usable comma/semicolon within the limit: force a hard cut at the last
  // word boundary (space) so we NEVER emit a chunk too long for Chrome.
  const lastSpace = limit.lastIndexOf(" ");
  if (lastSpace > 20) {
    const head = chunk.slice(0, lastSpace).trim();
    const tail = chunk.slice(lastSpace).trim() + (rest ? " " + rest : "");
    return { chunk: head, rest: tail };
  }
  // Fallback: hard character-slice at the limit as an absolute last resort.
  const cut = Math.min(MAX_UTTERANCE_LEN, chunk.length);
  return { chunk: chunk.slice(0, cut).trim(), rest: chunk.slice(cut).trim() + (rest ? " " + rest : "") };
}

/**
 * Reduce arbitrary-length text into a list of chunks each guaranteed to be
 * <= MAX_UTTERANCE_LEN, by repeatedly applying `splitAtComma` until nothing
 * remains. Unlike a single `splitAtComma` call, this is safe to use on text
 * of ANY length — which is required for `flush()`, where the leftover
 * buffer is seen only once (no while-loop re-processing like `enqueue()`
 * gets), so a single split can leave an oversized tail that Chrome then
 * drops silently.
 */
function splitIntoChunks(text: string): string[] {
  const chunks: string[] = [];
  let remaining = text.trim();

  while (remaining.length > MAX_UTTERANCE_LEN) {
    const { chunk, rest } = splitAtComma(remaining, "");
    if (chunk.length > 0) chunks.push(chunk);
    const nextRemaining = rest.trim();
    // Safety net: splitAtComma always shrinks progress given remaining.length
    // > MAX_UTTERANCE_LEN, but guard against any future edge case so this
    // can never spin forever.
    if (nextRemaining.length >= remaining.length) {
      if (nextRemaining.length > 0) chunks.push(nextRemaining);
      return chunks;
    }
    remaining = nextRemaining;
  }

  if (remaining.length > 0) chunks.push(remaining);
  return chunks;
}

/**
 * Resolve a stable English voice for pinning on every utterance.
 *
 * Remote cloud voices (e.g. "Google US English") stream audio from Google's
 * servers, and without an explicit voice fallback Chrome may randomly pick a
 * different/remote voice per utterance, stalling on odd characters. When no
 * en-US voice has loaded yet we return null and let the engine choose a
 * sensible default for that one utterance.
 */
function pickEnglishVoice(synth: SpeechSynthesis | null): SpeechSynthesisVoice | null {
  if (!synth) return null;
  const voices = synth.getVoices();
  if (voices && voices.length > 0) {
    // Prefer an exact en-US match, then any en-US, then any 'en', then any.
    for (const preferred of [VOICE_LANG, "en-US", "en"]) {
      const found = voices.find((v) => v.lang === preferred);
      if (found) return found;
    }
    // No explicit language preference at all — fall back to the first voice.
    return voices[0];
  }
  return null;
}

function useSpeechSynthesisInternal(
  onSpeak: ((text: string) => void) | undefined,
  onError: ((error: string) => void) | undefined,
) {
  const synthRef = useRef<SpeechSynthesis | null>(null);
  const voicesRef = useRef<SpeechSynthesisVoice[]>([]);

  const textBuffer = useRef<string>("");
  const utteranceQueue = useRef<SpeechSynthesisUtterance[]>([]);
  const isSpeakingRef = useRef(false);
  const [isSpeaking, setIsSpeaking] = useState(false);

  // Generation counter — bumped on stop/cancel to invalidate stale callbacks.
  const generationRef = useRef(0);

  // Timer for the inter-sentence pause (so we can cancel it on stop()).
  const pauseTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Pin the currently-playing utterance to a persistent ref for its ENTIRE
  // playback. In Chromium, passing an object to speechSynthesis.speak() does
  // NOT protect it from JavaScript Garbage Collection. A GC cycle while the
  // native C++ voice thread is speaking can destroy the JS wrapper mid-word,
  // so the engine silently stops (no error fired). Holding a reference here
  // pins the object until onend/onerror releases it.
  const activeUtteranceRef = useRef<SpeechSynthesisUtterance | null>(null);

  const setSpeaking = useCallback((val: boolean) => {
    isSpeakingRef.current = val;
    setIsSpeaking(val);
  }, []);

  // --- Setup / teardown ---
  useEffect(() => {
    if (typeof window === "undefined" || !("speechSynthesis" in window)) return;
    const synth = window.speechSynthesis;
    synthRef.current = synth;

    // Cache voices and re-cache whenever the list changes. This pins the
    // same voice object across utterances (avoiding random remote fallback).
    const cache = () => {
      const voices = synth.getVoices();
      voicesRef.current = voices as SpeechSynthesisVoice[];
    };
    cache();
    synth.onvoiceschanged = cache;

    return () => {
      generationRef.current++;
      try {
        synth.cancel();
      } catch {}
      voicesRef.current = [];
      synthRef.current = null;
    };
  }, []);

  // --- Speak the next utterance from the queue ---
  const speakNext = useCallback(() => {
    const synth = synthRef.current;
    if (!synth) return;
    if (utteranceQueue.current.length === 0) {
      // Queue is empty — done speaking.
      setSpeaking(false);
      return;
    }

    const utterance = utteranceQueue.current.shift()!;
    const gen = generationRef.current;

    // Pin the active utterance for its ENTIRE playback. Without this,
    // Chromium's GC can collect the JS wrapper while the native voice thread
    // is speaking it, causing a silent mid-word drop ("ghost drop") with no
    // error fired. Release it only on onend/onerror.
    activeUtteranceRef.current = utterance;

    setSpeaking(true);

    // Explicitly bind the utterance to the English voice we resolve.
    utterance.lang = VOICE_LANG;
    utterance.voice = pickEnglishVoice(synthRef.current);

    utterance.onstart = () => {
      if (gen === generationRef.current && onSpeak) {
        onSpeak(utterance.text);
      }
    };

    utterance.onend = () => {
      if (gen !== generationRef.current) return;
      activeUtteranceRef.current = null;
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
      activeUtteranceRef.current = null;
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
      activeUtteranceRef.current = null;
      setSpeaking(false);
      onError?.(err instanceof Error ? err.message : String(err));
    }
  }, [onSpeak, onError, setSpeaking]);

  // --- Queue a chunk and kick off speaking if idle ---
  const queueText = useCallback(
    (chunk: string) => {
      const text = chunk.trim();
      if (!text || text.length < 2) return;

      // Defensive backstop: no code path should ever reach here with text
      // over the limit, but if one does (now or after a future change),
      // split rather than silently feeding Chrome an utterance it will drop.
      if (text.length > MAX_UTTERANCE_LEN) {
        for (const piece of splitIntoChunks(text)) {
          queueText(piece);
        }
        return;
      }

      const utterance = new SpeechSynthesisUtterance(text);
      utterance.rate = SPEAK_RATE;
      utterance.pitch = 1.0;
      utterance.lang = VOICE_LANG;
      utterance.voice = pickEnglishVoice(synthRef.current);
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
  // BUG FIX: previously did a single splitAtComma() call, which can leave a
  // tail still over MAX_UTTERANCE_LEN (e.g. a long trailing sentence with no
  // early comma) — that oversized utterance would then be silently dropped
  // by Chrome, manifesting as missing words at the end of the speech.
  // splitIntoChunks() loops until every piece is within the limit.
  const flush = useCallback(() => {
    const buffer = textBuffer.current.trim();
    textBuffer.current = "";
    if (buffer.length > 0) {
      for (const piece of splitIntoChunks(buffer)) {
        queueText(piece);
      }
    }
  }, [queueText]);

  // --- Stop: cancel everything ---
  const stop = useCallback(() => {
    generationRef.current++;
    if (pauseTimerRef.current) {
      clearTimeout(pauseTimerRef.current);
      pauseTimerRef.current = null;
    }
    try {
      synthRef.current?.cancel();
    } catch {}
    textBuffer.current = "";
    utteranceQueue.current = [];
    activeUtteranceRef.current = null;
    setSpeaking(false);
  }, [setSpeaking]);

  return { isSpeaking, enqueue, flush, stop };
}

export function useSpeechSynthesis({
  onSpeak,
  onError,
}: UseSpeechSynthesisOptions = {}): UseSpeechSynthesisReturn {
  return useSpeechSynthesisInternal(onSpeak, onError);
}
