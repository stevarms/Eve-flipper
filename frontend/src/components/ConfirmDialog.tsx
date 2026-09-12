import { useEffect, useRef, useState } from "react";

interface ConfirmDialogProps {
  open: boolean;
  onClose: () => void;
  onConfirm: (value?: string) => void;
  title: string;
  message: string;
  confirmText?: string;
  cancelText?: string;
  variant?: "danger" | "default";
  /** If provided, shows an input field */
  inputType?: "text" | "number";
  inputDefaultValue?: string;
  inputPlaceholder?: string;
}

export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  message,
  confirmText = "OK",
  cancelText = "Cancel",
  variant = "default",
  inputType,
  inputDefaultValue = "",
  inputPlaceholder,
}: ConfirmDialogProps) {
  const overlayRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const [inputValue, setInputValue] = useState(inputDefaultValue);

  useEffect(() => {
    if (open) {
      setInputValue(inputDefaultValue);
      // Focus input or confirm button
      setTimeout(() => {
        if (inputType && inputRef.current) {
          inputRef.current.focus();
          inputRef.current.select();
        }
      }, 50);
    }
  }, [open, inputDefaultValue, inputType]);

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

  if (!open) return null;

  const handleConfirm = () => {
    if (inputType) {
      onConfirm(inputValue);
    } else {
      onConfirm();
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter") {
      e.preventDefault();
      handleConfirm();
    }
  };

  return (
    <div
      ref={overlayRef}
      className="fixed inset-0 z-[100] flex items-center justify-center bg-black/70 backdrop-blur-sm"
      onClick={(e) => {
        if (e.target === overlayRef.current) onClose();
      }}
      role="dialog"
      aria-modal="true"
      aria-labelledby="confirm-dialog-title"
    >
      <div className="max-w-md w-full mx-2 sm:mx-4 bg-eve-dark border border-eve-border rounded-sm shadow-2xl">
        {/* Header */}
        <div className="px-4 py-3 border-b border-eve-border bg-eve-panel">
          <h2 id="confirm-dialog-title" className="text-sm font-semibold uppercase tracking-wider text-eve-accent">
            {title}
          </h2>
        </div>

        {/* Content */}
        <div className="px-4 py-4">
          <p className="text-sm text-eve-text mb-4">{message}</p>

          {inputType && (
            <input
              ref={inputRef}
              type={inputType}
              value={inputValue}
              onChange={(e) => setInputValue(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder={inputPlaceholder}
              className="w-full px-3 py-2 bg-eve-input border border-eve-border rounded-sm text-eve-text text-sm
                         focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30
                         transition-colors [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
            />
          )}
        </div>

        {/* Actions */}
        <div className="flex justify-end gap-2 px-4 py-3 border-t border-eve-border bg-eve-panel">
          <button
            onClick={onClose}
            className="px-4 py-1.5 text-xs font-medium rounded-sm border border-eve-border
                       text-eve-dim hover:text-eve-text hover:border-eve-accent/50
                       transition-colors"
          >
            {cancelText}
          </button>
          <button
            onClick={handleConfirm}
            className={`px-4 py-1.5 text-xs font-semibold rounded-sm transition-colors
              ${variant === "danger"
                ? "bg-eve-error/80 text-white hover:bg-eve-error"
                : "bg-eve-accent text-eve-on-accent hover:bg-eve-accent-hover"
              }`}
          >
            {confirmText}
          </button>
        </div>
      </div>
    </div>
  );
}

// Hook for easier usage
export function useConfirmDialog() {
  const [state, setState] = useState<{
    open: boolean;
    title: string;
    message: string;
    confirmText?: string;
    cancelText?: string;
    variant?: "danger" | "default";
    inputType?: "text" | "number";
    inputDefaultValue?: string;
    inputPlaceholder?: string;
    resolve?: (value: string | boolean | null) => void;
  }>({
    open: false,
    title: "",
    message: "",
  });

  const confirm = (options: {
    title: string;
    message: string;
    confirmText?: string;
    cancelText?: string;
    variant?: "danger" | "default";
  }): Promise<boolean> => {
    return new Promise((resolve) => {
      setState({
        ...options,
        open: true,
        resolve: (v) => resolve(v as boolean),
      });
    });
  };

  const prompt = (options: {
    title: string;
    message: string;
    confirmText?: string;
    cancelText?: string;
    inputType?: "text" | "number";
    inputDefaultValue?: string;
    inputPlaceholder?: string;
  }): Promise<string | null> => {
    return new Promise((resolve) => {
      setState({
        ...options,
        open: true,
        resolve: (v) => resolve(v as string | null),
      });
    });
  };

  const handleClose = () => {
    state.resolve?.(state.inputType ? null : false);
    setState((s) => ({ ...s, open: false }));
  };

  const handleConfirm = (value?: string) => {
    state.resolve?.(state.inputType ? (value ?? "") : true);
    setState((s) => ({ ...s, open: false }));
  };

  const DialogComponent = (
    <ConfirmDialog
      open={state.open}
      onClose={handleClose}
      onConfirm={handleConfirm}
      title={state.title}
      message={state.message}
      confirmText={state.confirmText}
      cancelText={state.cancelText}
      variant={state.variant}
      inputType={state.inputType}
      inputDefaultValue={state.inputDefaultValue}
      inputPlaceholder={state.inputPlaceholder}
    />
  );

  return { confirm, prompt, DialogComponent };
}
