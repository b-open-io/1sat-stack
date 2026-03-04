import { useState, useCallback, useRef } from "react";

interface Toast {
  message: string;
  type: "success" | "error";
}

export function useToast() {
  const [toast, setToast] = useState<Toast | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout>>(undefined);

  const showToast = useCallback((message: string, type: "success" | "error" = "success") => {
    try {
      if (type === "error") {
        console.error("[toast]", message);
      } else {
        console.log("[toast]", message);
      }
    } catch (_) { /* console may be patched by devtools/extensions */ }
    if (timerRef.current) clearTimeout(timerRef.current);
    setToast({ message, type });
  }, []);

  return { toast, showToast };
}
