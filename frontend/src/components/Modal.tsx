import { useEffect, useRef, useId, useState } from "react";
import { createPortal } from "react-dom";

interface ModalProps {
  open: boolean;
  onClose: () => void;
  title: string;
  children: React.ReactNode;
  width?: string;
  allowFullscreen?: boolean;
  defaultFullscreen?: boolean;
  closeOnBackdrop?: boolean;
  showClose?: boolean;
}

export function Modal({
  open,
  onClose,
  title,
  children,
  width = "max-w-4xl",
  allowFullscreen = false,
  defaultFullscreen = false,
  closeOnBackdrop = false,
  showClose = true,
}: ModalProps) {
  const overlayRef = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const [mounted, setMounted] = useState(false);
  const [fullscreen, setFullscreen] = useState(defaultFullscreen);

  useEffect(() => {
    setMounted(true);
  }, []);

  useEffect(() => {
    const handleEsc = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    if (open) {
      document.addEventListener("keydown", handleEsc);
      document.body.style.overflow = "hidden";
    }
    return () => {
      document.removeEventListener("keydown", handleEsc);
      document.body.style.overflow = "";
    };
  }, [open, onClose]);

  if (!open || !mounted) return null;

  return createPortal(
    <div
      ref={overlayRef}
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      className="fixed inset-0 z-50 flex items-end sm:items-center justify-center bg-black/70 backdrop-blur-sm"
      onClick={(e) => {
        if (closeOnBackdrop && e.target === overlayRef.current) onClose();
      }}
    >
      <div
        className={
          fullscreen
            ? "w-[calc(100vw-12px)] h-[calc(100vh-12px)] sm:w-[calc(100vw-24px)] sm:h-[calc(100vh-24px)] max-w-none max-h-none flex flex-col bg-eve-dark border border-eve-border rounded-sm shadow-2xl"
            : `w-full ${width} mx-2 sm:mx-4 h-[95vh] sm:h-auto sm:max-h-[85vh] flex flex-col bg-eve-dark border border-eve-border rounded-t-lg sm:rounded-sm shadow-2xl`
        }
      >
        {/* Header */}
        <div className="flex items-center justify-between px-3 sm:px-4 py-2.5 sm:py-3 border-b border-eve-border bg-eve-panel shrink-0">
          <h2 id={titleId} className="text-xs sm:text-sm font-semibold uppercase tracking-wider text-eve-accent">
            {title}
          </h2>
          <div className="flex items-center gap-1">
            {allowFullscreen && (
              <button
                type="button"
                onClick={() => setFullscreen((v) => !v)}
                aria-label={fullscreen ? "Exit fullscreen" : "Fullscreen"}
                title={fullscreen ? "Exit fullscreen" : "Fullscreen"}
                className="text-eve-dim hover:text-eve-accent transition-colors text-sm leading-none p-1"
              >
                {fullscreen ? "□" : "▣"}
              </button>
            )}
            {showClose && (
              <button
                onClick={onClose}
                aria-label="Close dialog"
                className="text-eve-dim hover:text-eve-text transition-colors text-lg leading-none p-1"
              >
                &#10005;
              </button>
            )}
          </div>
        </div>
        {/* Content */}
        <div className="flex-1 min-h-0 overflow-auto">{children}</div>
      </div>
    </div>,
    document.body,
  );
}
