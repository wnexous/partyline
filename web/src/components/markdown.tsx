import { ImageIcon } from "lucide-react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

/*
 * Agent text as Markdown (UI-09). Raw HTML is never rendered: react-markdown turns it into
 * plain text (no rehype-raw), and its default URL transform drops javascript: and other unsafe
 * protocols. Images are shown as links so remote content never loads on its own.
 */

const components: Components = {
  a: ({ node: _node, href, children, ...props }) => (
    <a {...props} href={href} target="_blank" rel="noopener noreferrer nofollow">
      {children}
    </a>
  ),
  img: ({ src, alt }) => {
    const url = typeof src === "string" ? src : "";
    return (
      <a
        href={url}
        target="_blank"
        rel="noopener noreferrer nofollow"
        className="inline-flex items-center gap-1"
      >
        <ImageIcon className="size-3.5" aria-hidden="true" />
        {alt || url}
      </a>
    );
  },
};

export default function Markdown({ text }: { text: string }) {
  return (
    <div className="message-markdown">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {text}
      </ReactMarkdown>
    </div>
  );
}
