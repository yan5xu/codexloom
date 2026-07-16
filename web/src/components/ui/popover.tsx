import * as React from "react";
import { Popover as PopoverPrimitive } from "@base-ui/react/popover";

import { cn } from "@/lib/utils";

function Popover(props: PopoverPrimitive.Root.Props) {
  return <PopoverPrimitive.Root data-slot="popover" {...props} />;
}

function PopoverTrigger(props: PopoverPrimitive.Trigger.Props) {
  return <PopoverPrimitive.Trigger data-slot="popover-trigger" {...props} />;
}

function PopoverContent({
  className,
  children,
  align = "start",
  side = "bottom",
  sideOffset = 5,
  ...props
}: PopoverPrimitive.Popup.Props & Pick<PopoverPrimitive.Positioner.Props, "align" | "side" | "sideOffset">) {
  return (
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Positioner className="z-50 outline-none" align={align} side={side} sideOffset={sideOffset}>
        <PopoverPrimitive.Popup
          data-slot="popover-content"
          initialFocus={false}
          className={cn(
            "w-80 rounded-md border border-border bg-popover p-3 text-popover-foreground shadow-card outline-none data-closed:hidden",
            className,
          )}
          {...props}
        >
          {children}
        </PopoverPrimitive.Popup>
      </PopoverPrimitive.Positioner>
    </PopoverPrimitive.Portal>
  );
}

const PopoverClose = PopoverPrimitive.Close;

export { Popover, PopoverClose, PopoverContent, PopoverTrigger };
