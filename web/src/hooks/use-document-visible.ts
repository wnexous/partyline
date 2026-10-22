import { useEffect, useState } from "react";

const isVisible = () => document.visibilityState !== "hidden";

/** Whether the tab is showing: false while another tab or a minimised window hides it. */
export function useDocumentVisible(): boolean {
  const [visible, setVisible] = useState(isVisible);
  useEffect(() => {
    const onChange = () => setVisible(isVisible());
    document.addEventListener("visibilitychange", onChange);
    onChange();
    return () => document.removeEventListener("visibilitychange", onChange);
  }, []);
  return visible;
}
