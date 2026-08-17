import { useEffect, useState } from "react";
import type { CustomRendererProps } from "streamdown";

export const PLANTUML_SERVER_URL = "https://www.plantuml.com/plantuml/svg/";

function encode6Bit(value: number) {
  if (value < 10) return String.fromCharCode(48 + value);
  if (value < 36) return String.fromCharCode(65 + value - 10);
  if (value < 62) return String.fromCharCode(97 + value - 36);
  if (value === 62) return "-";
  if (value === 63) return "_";
  return "?";
}

function append3Bytes(first: number, second: number, third: number) {
  return [
    first >> 2,
    ((first & 0x3) << 4) | (second >> 4),
    ((second & 0xf) << 2) | (third >> 6),
    third & 0x3f,
  ].map(encode6Bit).join("");
}

/**
 * PlantUML's URL alphabet is base64-like, but it consumes a raw DEFLATE
 * stream. The stored-block fallback keeps the client dependency-free when a
 * browser does not expose CompressionStream; Chromium uses compressed output.
 */
export function encodePlantUMLBytes(bytes: Uint8Array) {
  let encoded = "";
  for (let index = 0; index < bytes.length; index += 3) {
    const remaining = bytes.length - index;
    encoded += append3Bytes(bytes[index], remaining > 1 ? bytes[index + 1] : 0, remaining > 2 ? bytes[index + 2] : 0).slice(0, remaining === 1 ? 2 : remaining === 2 ? 3 : 4);
  }
  return encoded;
}

function storedDeflate(bytes: Uint8Array) {
  const output: number[] = [];
  if (bytes.length === 0) return Uint8Array.from([1, 0, 0, 0xff, 0xff]);

  for (let offset = 0; offset < bytes.length; offset += 0xffff) {
    const length = Math.min(0xffff, bytes.length - offset);
    const finalBlock = offset + length >= bytes.length;
    output.push(finalBlock ? 1 : 0, length & 0xff, length >>> 8, (~length) & 0xff, ((~length) >>> 8) & 0xff);
    for (let index = 0; index < length; index += 1) output.push(bytes[offset + index]);
  }
  return Uint8Array.from(output);
}

async function compressionStreamDeflate(bytes: Uint8Array, format: "deflate" | "deflate-raw") {
  if (typeof CompressionStream === "undefined") return null;

  try {
    const stream = new CompressionStream(format as unknown as CompressionFormat);
    const compressed = await new Response(new Blob([bytes]).stream().pipeThrough(stream)).arrayBuffer();
    return new Uint8Array(compressed);
  } catch {
    return null;
  }
}

async function rawDeflate(bytes: Uint8Array) {
  const raw = await compressionStreamDeflate(bytes, "deflate-raw");
  if (raw) return raw;

  const wrapped = await compressionStreamDeflate(bytes, "deflate");
  if (wrapped && wrapped.length > 6) return wrapped.slice(2, -4);

  return storedDeflate(bytes);
}

export async function plantUMLImageURL(source: string) {
  const bytes = new TextEncoder().encode(source);
  const compressed = await rawDeflate(bytes);
  return `${PLANTUML_SERVER_URL}${encodePlantUMLBytes(compressed)}`;
}

function SourceFallback({ source }: { source: string }) {
  return (
    <details className="mt-2 min-w-0 text-[11px] text-muted-foreground">
      <summary className="cursor-pointer select-none font-mono hover:text-foreground">Show PlantUML source</summary>
      <pre className="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-md border border-border/70 bg-muted/25 p-3 font-mono text-[11px] leading-5 text-foreground/80">
        {source}
      </pre>
    </details>
  );
}

function PlantUMLBlock({ code, isIncomplete }: CustomRendererProps) {
  const source = code.replace(/\n$/, "");
  const [imageURL, setImageURL] = useState<string | null>(null);
  const [error, setError] = useState(false);
  const [retry, setRetry] = useState(0);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    if (isIncomplete) return;
    let active = true;
    setImageURL(null);
    setError(false);
    setLoaded(false);
    void plantUMLImageURL(source).then((url) => {
      if (active) setImageURL(url);
    }).catch(() => {
      if (active) setError(true);
    });
    return () => {
      active = false;
    };
  }, [isIncomplete, source]);

  if (isIncomplete) {
    return (
      <pre className="my-3 max-w-full overflow-auto rounded-md border border-border/70 bg-muted/25 p-3 font-mono text-[12.5px] leading-5">
        <code>{code}</code>
      </pre>
    );
  }

  const retryRender = () => {
    setRetry((attempt) => attempt + 1);
    setError(false);
    setLoaded(false);
  };

  return (
    <figure className="my-4 min-w-0 max-w-full overflow-hidden rounded-md border border-border/70 bg-card p-3" data-plantuml-block data-plantuml-state={error ? "error" : loaded ? "loaded" : "loading"}>
      {!error && !loaded && (
        <div className="flex min-h-16 items-center justify-center gap-2 text-[12px] text-muted-foreground" role="status" aria-live="polite">
          <span className="size-3 animate-pulse rounded-full bg-primary/60" aria-hidden="true" />
          Loading PlantUML diagram…
        </div>
      )}
      {error && (
        <div className="space-y-2 rounded-md border border-destructive/30 bg-destructive/5 p-3" role="alert">
          <p className="text-[12px] leading-5 text-destructive">PlantUML diagram could not be loaded.</p>
          <button type="button" className="rounded-md border border-border px-2.5 py-1.5 text-[11px] font-medium text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40" onClick={retryRender}>
            Retry PlantUML diagram
          </button>
        </div>
      )}
      {imageURL && !error && (
        <img
          key={`${imageURL}:${retry}`}
          src={imageURL}
          alt="PlantUML diagram"
          className={`mx-auto block h-auto max-w-full${loaded ? "" : " invisible absolute"}`}
          onLoad={() => setLoaded(true)}
          onError={() => setError(true)}
          referrerPolicy="no-referrer"
        />
      )}
      <SourceFallback source={source} />
    </figure>
  );
}

export const plantumlRenderer = {
  language: ["plantuml", "puml"],
  component: PlantUMLBlock,
};
