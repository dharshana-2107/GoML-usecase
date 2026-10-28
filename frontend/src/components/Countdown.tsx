import { useEffect, useState } from "react";

interface Props {
  startsAt: number;
  now: () => number; // server-synced clock
  onDone?: () => void; // fired when the clock hits zero
}

const pad = (n: number) => String(n).padStart(2, "0");

// hours / minutes / seconds tiles. ticks four times a second so the page
// switches to "live" within 250ms of the start time.
export function Countdown({ startsAt, now, onDone }: Props) {
  const [left, setLeft] = useState(() => startsAt - now());

  useEffect(() => {
    const id = setInterval(() => {
      const ms = startsAt - now();
      setLeft(ms);
      if (ms <= 0) {
        clearInterval(id);
        onDone?.();
      }
    }, 250);
    return () => clearInterval(id);
  }, [startsAt, now, onDone]);

  const total = Math.max(0, Math.ceil(left / 1000));
  const parts: [number, string][] = [
    [Math.floor(total / 3600), "hours"],
    [Math.floor((total % 3600) / 60), "minutes"],
    [total % 60, "seconds"],
  ];

  return (
    <div className="countdown" role="timer" aria-label={`${parts.map(([n, l]) => `${n} ${l}`).join(", ")} until the sale starts`}>
      {parts.map(([n, label]) => (
        <span className="tile" key={label} aria-hidden="true">
          <strong>{pad(n)}</strong>
          <small>{label}</small>
        </span>
      ))}
    </div>
  );
}
