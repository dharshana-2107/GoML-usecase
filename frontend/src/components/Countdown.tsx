import { useEffect, useState } from "react";

interface Props {
  startsAt: number;
  now: () => number; // server-synced clock
  onFinalSeconds?: () => void; // fired once, 15s before start
  onDone?: () => void; // fired when the clock hits zero
}

// ticks four times a second so it flips to "live" within 250ms of 8:00 pm
export function Countdown({ startsAt, now, onFinalSeconds, onDone }: Props) {
  const [left, setLeft] = useState(() => startsAt - now());

  useEffect(() => {
    let fired = false;
    const id = setInterval(() => {
      const ms = startsAt - now();
      setLeft(ms);
      if (!fired && ms <= 15_000) {
        fired = true;
        onFinalSeconds?.();
      }
      if (ms <= 0) {
        clearInterval(id);
        onDone?.();
      }
    }, 250);
    return () => clearInterval(id);
  }, [startsAt, now, onFinalSeconds, onDone]);

  if (left <= 0) return null;
  const total = Math.ceil(left / 1000);
  const parts = [Math.floor(total / 3600), Math.floor((total % 3600) / 60), total % 60];

  return (
    <div className="countdown" role="timer" aria-label="time until the sale starts">
      {parts.map((n, i) => (
        <span key={i} className="countdown-cell">
          <strong>{String(n).padStart(2, "0")}</strong>
          <small>{["hrs", "min", "sec"][i]}</small>
        </span>
      ))}
    </div>
  );
}
