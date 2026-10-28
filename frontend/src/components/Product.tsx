// the product: picture and specs on the left, name, price and stock on the right.
// change the details here and the banner and buy box follow.
import type { ReactNode } from "react";

export const TOTAL_UNITS = 500;
export const PRODUCT = "X Phone Ultra";
const PRICE = 24999;
const MRP = 39999;
const OFF = Math.floor((1 - PRICE / MRP) * 100);
const inr = (n: number) => `₹${n.toLocaleString("en-IN")}`;
export const PRICE_TEXT = inr(PRICE);

// "8:00 pm" in the shopper's own time zone, from the server's start time
export const formatTime = (ms: number) =>
  new Date(ms).toLocaleTimeString("en-IN", { hour: "numeric", minute: "2-digit" });

// left side: product shot plus the three facts people compare phones on
export function ProductShowcase() {
  return (
    <div className="showcase">
      <div className="shot">
        <PhoneArt />
      </div>
      <ul className="highlights">
        <li>6.7″ AMOLED, 120 Hz</li>
        <li>256 GB storage</li>
        <li>5,000 mAh battery</li>
      </ul>
    </div>
  );
}

// right side: name, price, stock, then whatever action fits (sign in or book)
export function BuyBox({ remaining, children }: { remaining: number; children: ReactNode }) {
  const pct = (remaining / TOTAL_UNITS) * 100;
  return (
    <div className="buybox">
      <p className="brandline">X Phone</p>
      <h2 className="name">{PRODUCT} 5G</h2>
      <p className="variant">Ocean Blue, 256 GB</p>
      <div className="pricing">
        <span className="price">{PRICE_TEXT}</span>
        <span className="mrp">
          M.R.P. <s>{inr(MRP)}</s>
        </span>
        <span className="off">{OFF}% off</span>
      </div>
      <p className="tax">Inclusive of all taxes. Free delivery.</p>

      <div className="stock">
        <div className="stock-bar" aria-hidden="true">
          <span style={{ width: `${pct}%` }} />
        </div>
        <p>{remaining > 0 ? `${remaining} of ${TOTAL_UNITS} left. Limit 1 per customer.` : "Out of stock"}</p>
      </div>

      {children}
    </div>
  );
}

// front and back of the phone, drawn inline: no image download
function PhoneArt() {
  return (
    <svg viewBox="0 0 360 430" role="img" aria-label={`${PRODUCT} in Ocean Blue, front and back`}>
      <defs>
        <linearGradient id="scr" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#1f5bd6" />
          <stop offset="1" stopColor="#4fd18b" />
        </linearGradient>
      </defs>
      {/* back of the phone, tilted behind */}
      <g transform="rotate(-9 110 220)">
        <rect x="30" y="40" width="160" height="350" rx="30" fill="#c9dbfb" />
        <rect x="48" y="58" width="70" height="92" rx="20" fill="#0e2240" />
        <circle cx="72" cy="82" r="13" fill="#2c4470" />
        <circle cx="72" cy="126" r="13" fill="#2c4470" />
        <circle cx="100" cy="104" r="6" fill="#e3f7ec" />
      </g>
      {/* front of the phone */}
      <rect x="150" y="20" width="180" height="392" rx="34" fill="#0e2240" />
      <rect x="160" y="30" width="160" height="372" rx="26" fill="url(#scr)" />
      <circle cx="240" cy="50" r="6" fill="#0e2240" />
      <rect x="330" y="120" width="4" height="54" rx="2" fill="#0e2240" />
      <text x="240" y="150" textAnchor="middle" fill="#fff" fontSize="46" fontWeight="700" fontFamily="system-ui">
        8:00
      </text>
      <circle cx="210" cy="300" r="70" fill="#fff" opacity="0.08" />
      <circle cx="290" cy="340" r="50" fill="#fff" opacity="0.1" />
    </svg>
  );
}
