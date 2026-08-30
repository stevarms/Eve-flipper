import * as React from "react";
import * as TabsPrimitive from "@radix-ui/react-tabs";
import { cn } from "@/lib/utils";

/**
 * Tabs (Radix).
 *
 * Two jobs in this app, both from the EveBoosters pattern:
 *  - tabbed FILTERS (General / Profit / Risk / My Assets), so a 12-field form
 *    shows three fields at a time instead of eating two-thirds of the screen;
 *  - result VIEWS over one table (Opportunities / Watching / Trading), so
 *    workflow states stop becoming separate tools.
 */
export const Tabs = TabsPrimitive.Root;

export const TabsList = React.forwardRef<
  React.ElementRef<typeof TabsPrimitive.List>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.List>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.List
    ref={ref}
    className={cn("inline-flex items-center gap-0.5 border-b border-eve-border", className)}
    {...props}
  />
));
TabsList.displayName = TabsPrimitive.List.displayName;

export const TabsTrigger = React.forwardRef<
  React.ElementRef<typeof TabsPrimitive.Trigger>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.Trigger>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.Trigger
    ref={ref}
    className={cn(
      "relative -mb-px inline-flex h-7 items-center whitespace-nowrap rounded-t-sm px-3",
      "font-ui text-t-body font-medium text-fg-tertiary transition-colors",
      "hover:text-fg-secondary",
      "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-eve-accent",
      "disabled:pointer-events-none disabled:opacity-50",
      "data-[state=active]:border-b-2 data-[state=active]:border-eve-accent data-[state=active]:text-fg",
      className,
    )}
    {...props}
  />
));
TabsTrigger.displayName = TabsPrimitive.Trigger.displayName;

export const TabsContent = React.forwardRef<
  React.ElementRef<typeof TabsPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.Content>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.Content
    ref={ref}
    className={cn("focus-visible:outline-none", className)}
    {...props}
  />
));
TabsContent.displayName = TabsPrimitive.Content.displayName;
