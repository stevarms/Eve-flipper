import * as React from "react";
import { cn } from "@/lib/utils";

/**
 * Input.
 *
 * `numeric` switches to tabular figures and right-alignment — use it for every
 * ISK / percentage / quantity field so digits line up with the table columns
 * they filter.
 */
export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  numeric?: boolean;
}

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ className, numeric = false, ...props }, ref) => (
    <input
      ref={ref}
      data-numeric={numeric || undefined}
      className={cn(
        "h-8 w-full rounded-sm border border-eve-border bg-surface-3 px-2",
        "font-ui text-t-body text-fg placeholder:text-fg-tertiary",
        "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-eve-accent",
        "disabled:cursor-not-allowed disabled:opacity-50",
        numeric && "text-right",
        className,
      )}
      {...props}
    />
  ),
);
Input.displayName = "Input";
