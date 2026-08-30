import * as React from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * Sheet — a right-hand drawer built on Radix Dialog.
 *
 * This is tier 2 of the three-tier disclosure rule (docs/UI_DESIGN_SYSTEM.md):
 * the grid shows at most 6 columns to DECIDE with, and clicking a row opens
 * this to INSPECT everything else — the remaining columns grouped into
 * sections, the order book, price history, and the reasoning behind a score.
 *
 * It is how we cut Station Trade's 20 columns down to 6 without losing a
 * single number.
 *
 * Footer convention (docs/UI_AUDIT.md finding A6): right-aligned, primary
 * action rightmost, secondary to its left, destructive far-left.
 */
export const Sheet = DialogPrimitive.Root;
export const SheetTrigger = DialogPrimitive.Trigger;
export const SheetClose = DialogPrimitive.Close;
export const SheetTitle = DialogPrimitive.Title;
export const SheetDescription = DialogPrimitive.Description;

const SheetOverlay = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Overlay
    ref={ref}
    className={cn(
      "fixed inset-0 z-40 bg-black/50",
      "data-[state=open]:animate-in data-[state=open]:fade-in-0",
      "data-[state=closed]:animate-out data-[state=closed]:fade-out-0",
      className,
    )}
    {...props}
  />
));
SheetOverlay.displayName = DialogPrimitive.Overlay.displayName;

export interface SheetContentProps
  extends Omit<React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content>, "title"> {
  /** Accessible name. Required — Radix warns without a Title.
   *  `title` is omitted from the base props because the native HTML
   *  attribute is `string`, and we want to allow rich nodes here. */
  title: React.ReactNode;
  /** Optional line under the title: what this row is, in plain English. */
  description?: React.ReactNode;
  /** Right-aligned action row pinned to the bottom. */
  footer?: React.ReactNode;
  width?: string;
}

export const SheetContent = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Content>,
  SheetContentProps
>(({ className, children, title, description, footer, width = "w-[520px]", ...props }, ref) => (
  <DialogPrimitive.Portal>
    <SheetOverlay />
    <DialogPrimitive.Content
      ref={ref}
      className={cn(
        "fixed inset-y-0 right-0 z-50 flex max-w-full flex-col",
        "border-l border-eve-border bg-surface-1 shadow-2xl",
        "data-[state=open]:animate-in data-[state=open]:slide-in-from-right",
        "data-[state=closed]:animate-out data-[state=closed]:slide-out-to-right",
        width,
        className,
      )}
      {...props}
    >
      <div className="flex items-start gap-3 border-b border-eve-border px-4 py-3">
        <div className="min-w-0 flex-1">
          <DialogPrimitive.Title className="truncate font-ui text-t-title font-semibold text-fg">
            {title}
          </DialogPrimitive.Title>
          {description ? (
            <DialogPrimitive.Description className="mt-0.5 font-ui text-t-caption text-fg-tertiary">
              {description}
            </DialogPrimitive.Description>
          ) : null}
        </div>
        <DialogPrimitive.Close
          aria-label="Close"
          className={cn(
            "shrink-0 rounded-sm p-1 text-fg-tertiary transition-colors",
            "hover:bg-surface-2 hover:text-fg",
            "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-eve-accent",
          )}
        >
          <X className="h-4 w-4" />
        </DialogPrimitive.Close>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">{children}</div>

      {footer ? (
        <div className="flex items-center justify-end gap-2 border-t border-eve-border px-4 py-3">
          {footer}
        </div>
      ) : null}
    </DialogPrimitive.Content>
  </DialogPrimitive.Portal>
));
SheetContent.displayName = DialogPrimitive.Content.displayName;
