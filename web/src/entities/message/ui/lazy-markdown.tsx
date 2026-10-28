import { lazy } from "react";

/** The Markdown renderer, loaded on first use: it is the heaviest part of a timeline row. */
export const LazyMarkdown = lazy(() => import("./markdown"));
