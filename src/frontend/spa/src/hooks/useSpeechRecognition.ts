/**
 * English-only Web Speech API speech-recognition wrapper.
 *
 * Wraps the (non-standardized, vendor-prefixed) Web Speech API
 * `SpeechRecognition` interface, which is supported in Chromium-based browsers
 * and Safari (and only when the page is served over https:// or localhost).
 *
 * Uses continuous mode (continuous=true, interimResults=true) so the listener
 * keeps running across pauses: as the user pauses, each phrase is committed
 * by the engine and appended incrementally (see the `onresult` handler
 * below). This lets the user speak slowly or dictate continuously without
 * being cut off mid-sentence.
 *
 * Transcripts are delivered through the `onTranscript` callback (rather than
 * returned state) so the caller can append directly to its input value.
 */

import { useCallback, useEffect, useRef, useState } from "react";

/** Minimal shape of a single SpeechRecognition result entry. */
interface SpeechRecognitionAlternative {
  transcript: string;
  confidence: number;
}

/** Minimal shape of the results array returned on `onresult`. */
interface SpeechRecognitionResult {
  isFinal: boolean;
  [index: number]: SpeechRecognitionAlternative;
}

interface SpeechRecognitionResultList {
  length: number;
  [index: number]: SpeechRecognitionResult;
}

interface SpeechRecognitionInstance {
  lang: string;
  continuous: boolean;
  interimResults: boolean;
  maxAlternatives: number;
  start(): void;
  stop(): void;
  abort(): void;
  onresult: ((event: { results: SpeechRecognitionResultList }) => void) | null;
  onend: (() => void) | null;
  onerror: ((event: { error: string }) => void) | null;
}

/** A 0-arg constructor reference for the (vendor-prefixed) API. */
type SpeechRecognitionCtor = new () => SpeechRecognitionInstance;

/**
 * Resolves the available SpeechRecognition constructor across browsers.
 * - `window.SpeechRecognition`  (Chrome, Edge, Safari expose it un-prefixed)
 * - `webkitSpeechRecognition`   (older Safari / Chrome)
 */
function getCtor(): SpeechRecognitionCtor | undefined {
  const w = window as unknown as {
    SpeechRecognition?: SpeechRecognitionCtor;
    webkitSpeechRecognition?: SpeechRecognitionCtor;
  };
  return w.SpeechRecognition || w.webkitSpeechRecognition;
}

const SUPPORTED_LANG = "en-US";

/** Translate the raw API error codes into user-friendly messages. */
function describeError(error: string): string {
  switch (error) {
    case "no-speech":
      return "No speech detected — try speaking a phrase and again to stop.";
    case "audio-capture":
      return "No microphone found. Please connect one and retry.";
    case "not-allowed":
      return "Microphone access denied. Allow microphone access to use voice input.";
    default:
      return `Speech recognition error (${error}).`;
  }
}

export interface SpeechRecognitionHandle {
  /** Whether the Web Speech API is available in this browser. */
  isSupported: boolean;
  /** Whether a recognition session is currently active. */
  isListening: boolean;
  /** A human-readable error message, or null. */
  error: string | null;
  /** Begin a continuous recognition session (keeps listening).*/
  start: () => void;
  /** Stop the current recognition session (also a no-op if already stopped).*/
  stop: () => void;
}

export function useSpeechRecognition(
  onTranscript: (text: string) => void
): SpeechRecognitionHandle {
  const Ctor = getCtor();
  const isSupported = typeof Ctor === "function";

  const [isListening, setIsListening] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const recognitionRef = useRef<SpeechRecognitionInstance | null>(null);

  // Tracks which result indices have already been committed as final.
  // In continuous mode the `results` list is live: it grows when a new
  // phrase starts and existing results are updated in place as the engine
  // refines the interim transcript. We must only process *new* results
  // (beyond what we've already seen) and only commit them once they turn
  // final — otherwise the same phrase gets appended multiple times.
  const committedFinalIndicesRef = useRef<Set<number>>(new Set());

  const start = useCallback(() => {
    if (!isSupported) return;
    setError(null);

    // Cancel any in-flight session before starting a new one.
    recognitionRef.current?.abort();
    committedFinalIndicesRef.current.clear();

    const recognition = new Ctor!();
    recognition.lang = SUPPORTED_LANG;
    recognition.continuous = true;
    recognition.interimResults = true;
    recognition.maxAlternatives = 1;

    recognition.onresult = (event) => {
      // `event.results` is a live list containing ALL results from the
      // current session. We only care about results that are NEW
      // (beyond what we've already processed) AND have just become
      // final (the user paused long enough for the engine to commit
      // that phrase).
      let newFinalText = "";
      const committed = committedFinalIndicesRef.current;
      for (let i = 0; i < event.results.length; i++) {
        const result = event.results[i];
        if (result.isFinal && !committed.has(i)) {
          newFinalText += result[0]?.transcript ?? "";
          committed.add(i);
        }
      }

      if (newFinalText) {
        const text = newFinalText.trim();
        if (text) {
          onTranscript(text);
        }
      }
    };

    recognition.onend = () => {
      setIsListening(false);
      committedFinalIndicesRef.current.clear();
      // Deliberately do NOT auto-restart: single-session-per-click.
    };

    recognition.onerror = (event) => {
      setIsListening(false);
      setError(describeError(event.error));
    };

    recognitionRef.current = recognition;

    try {
      recognition.start();
      setIsListening(true);
    } catch {
      setIsListening(false);
      setError("Failed to start speech recognition.");
    }
  }, [isSupported]);

  const stop = useCallback(() => {
    recognitionRef.current?.stop();
  }, []);

  useEffect(() => {
    return () => {
      recognitionRef.current?.abort();
    };
  }, []);

  return { isSupported, isListening, error, start, stop };
}
