type BrandMarkProps = {
  className?: string;
  monochrome?: boolean;
  title?: string;
};

export function BrandMark({ className = "size-6", monochrome = false, title }: BrandMarkProps) {
  if (monochrome) {
    return (
      <span
        className={`inline-block shrink-0 bg-current ${className}`}
        role={title ? "img" : undefined}
        aria-hidden={title ? undefined : true}
        aria-label={title}
        style={{
          WebkitMaskImage: "url('/codexloom-mark.png')",
          maskImage: "url('/codexloom-mark.png')",
          WebkitMaskPosition: "center",
          maskPosition: "center",
          WebkitMaskRepeat: "no-repeat",
          maskRepeat: "no-repeat",
          WebkitMaskSize: "contain",
          maskSize: "contain",
        }}
      />
    );
  }

  return (
    <span
      className={`inline-flex shrink-0 items-center justify-center ${className}`}
      role={title ? "img" : undefined}
      aria-hidden={title ? undefined : true}
      aria-label={title}
    >
      <img src="/codexloom-mark.png" alt="" className="h-full w-full object-contain dark:hidden" />
      <img src="/codexloom-mark-dark.png" alt="" className="hidden h-full w-full object-contain dark:block" />
    </span>
  );
}

export function BrandLockup({ compact = false }: { compact?: boolean }) {
  return (
    <div className="flex min-w-0 items-center gap-2.5">
      <BrandMark className={compact ? "size-7 shrink-0" : "size-10 shrink-0"} title="CodexLoom" />
      <div className="min-w-0">
        <div className={`${compact ? "text-[17px]" : "text-[28px]"} truncate font-serif leading-none text-foreground`}>
          CodexLoom
        </div>
        {!compact ? (
          <div className="mt-1.5 text-[10.5px] text-muted-foreground">Long-lived threads, woven into an agent organization.</div>
        ) : null}
      </div>
    </div>
  );
}
