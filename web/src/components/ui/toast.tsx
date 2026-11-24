import { Toast as ToastPrimitive } from "@base-ui/react/toast"
import { cn } from "@/lib/utils"

/**
 * Fixed to the top left corner, 16 px from the top and left edges. Under 640 px
 * it spans the window with 16 px margins. z-40 keeps it under modal dialogs
 * (z-50) and over page content.
 */
function ToastViewport({ className, ...props }: ToastPrimitive.Viewport.Props) {
  return (
    <ToastPrimitive.Viewport
      data-slot="toast-viewport"
      className={cn(
        "fixed top-4 right-4 left-4 z-40 flex flex-col gap-2 outline-none sm:right-auto sm:w-96",
        className
      )}
      {...props}
    />
  )
}

/** Fades in and out; the slide only runs when the user has not asked for reduced motion. */
function ToastRoot({ className, ...props }: ToastPrimitive.Root.Props) {
  return (
    <ToastPrimitive.Root
      data-slot="toast"
      className={cn(
        "relative grid gap-1 rounded-lg border border-border bg-popover p-3 pr-10 text-sm text-popover-foreground shadow-md outline-none transition-opacity duration-200 focus-visible:ring-3 focus-visible:ring-ring/50 data-starting-style:opacity-0 data-ending-style:opacity-0 motion-safe:transition-[opacity,transform] motion-safe:data-starting-style:-translate-x-3 motion-safe:data-ending-style:-translate-x-3",
        className
      )}
      {...props}
    />
  )
}

function ToastTitle({ className, ...props }: ToastPrimitive.Title.Props) {
  return (
    <ToastPrimitive.Title
      data-slot="toast-title"
      className={cn("font-medium", className)}
      {...props}
    />
  )
}

function ToastDescription({ className, ...props }: ToastPrimitive.Description.Props) {
  return (
    <ToastPrimitive.Description
      data-slot="toast-description"
      className={cn("text-muted-foreground", className)}
      {...props}
    />
  )
}

function ToastAction({ className, ...props }: ToastPrimitive.Action.Props) {
  return (
    <ToastPrimitive.Action
      data-slot="toast-action"
      className={cn(
        "mt-1 inline-flex h-7 w-fit items-center rounded-lg border border-border bg-background px-2.5 text-[0.8rem] font-medium outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50",
        className
      )}
      {...props}
    />
  )
}

function ToastClose({ className, ...props }: ToastPrimitive.Close.Props) {
  return (
    <ToastPrimitive.Close
      data-slot="toast-close"
      // Base UI hides the close button from assistive tech until the stack is
      // expanded; this list is never collapsed, so it is always available.
      aria-hidden={false}
      aria-label="Dismiss notification"
      className={cn(
        "absolute top-2 right-2 inline-flex size-6 items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50",
        className
      )}
      {...props}
    >
      <span aria-hidden="true">×</span>
    </ToastPrimitive.Close>
  )
}

export { ToastAction, ToastClose, ToastDescription, ToastRoot, ToastTitle, ToastViewport }
