import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { TriangleAlert, X } from "lucide-react";

export interface ConfirmOptions {
  title: string;
  /** Body text. Keep it to what actually happens if they say yes. */
  message?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  /** Marks the action as destructive: red confirm button and a warning glyph. */
  danger?: boolean;
}

type Resolver = (ok: boolean) => void;

const Ctx = createContext<((o: ConfirmOptions) => Promise<boolean>) | null>(null);

/**
 * Replaces window.confirm with an in-page dialog.
 *
 * The promise-based API keeps call sites as short as the native one
 * (`if (!(await confirm({...}))) return;`) while letting the dialog match the
 * rest of the interface.
 */
export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [opts, setOpts] = useState<ConfirmOptions | null>(null);
  const resolver = useRef<Resolver | null>(null);
  const confirmBtn = useRef<HTMLButtonElement>(null);

  const confirm = useCallback((o: ConfirmOptions) => {
    setOpts(o);
    return new Promise<boolean>((resolve) => {
      resolver.current = resolve;
    });
  }, []);

  const settle = useCallback((ok: boolean) => {
    resolver.current?.(ok);
    resolver.current = null;
    setOpts(null);
  }, []);

  // Escape cancels, Enter confirms — the same reflexes the native dialog has.
  useEffect(() => {
    if (!opts) return;
    confirmBtn.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        settle(false);
      } else if (e.key === "Enter") {
        e.preventDefault();
        settle(true);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [opts, settle]);

  return (
    <Ctx.Provider value={confirm}>
      {children}
      {opts && (
        <div className="modal-scrim confirm-scrim" onClick={() => settle(false)}>
          <div
            className="modal confirm"
            role="alertdialog"
            aria-modal="true"
            aria-labelledby="confirm-title"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="modal-head">
              {opts.danger && (
                <TriangleAlert size={15} strokeWidth={1.8} className="confirm-glyph" />
              )}
              <h2 id="confirm-title">{opts.title}</h2>
              <span style={{ marginLeft: "auto" }}>
                <button
                  className="icon-btn"
                  onClick={() => settle(false)}
                  aria-label="Cancel"
                >
                  <X size={15} strokeWidth={1.7} />
                </button>
              </span>
            </div>

            {opts.message && (
              <div className="modal-body">
                <p className="prose" style={{ margin: 0 }}>
                  {opts.message}
                </p>
              </div>
            )}

            <div className="modal-foot">
              <div className="right">
                <button className="btn" onClick={() => settle(false)}>
                  {opts.cancelLabel ?? "Cancel"}
                </button>
                <button
                  ref={confirmBtn}
                  className={`btn ${opts.danger ? "destructive" : "primary"}`}
                  onClick={() => settle(true)}
                >
                  {opts.confirmLabel ?? "Confirm"}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </Ctx.Provider>
  );
}

/** Returns an async confirm(). Throws if used outside ConfirmProvider. */
export function useConfirm() {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useConfirm must be used inside ConfirmProvider");
  return ctx;
}
