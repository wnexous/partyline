/*
 * Whether a drag carries files (not text or a link): only those are taken by the composer's drop
 * zone, so dragging text still lands in the text box as the browser means it to.
 */
export const carriesFiles = (data: DataTransfer | null) =>
  !!data && (data.files.length > 0 || Array.from(data.types).includes("Files"));
