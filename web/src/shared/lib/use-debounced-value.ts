import { useEffect, useState } from "react";

/** `value` once it has stopped changing for `delay` ms: a search box asks the server when typing settles. */
export function useDebouncedValue<T>(value: T, delay = 250): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setSettled(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return settled;
}
