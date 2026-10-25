/**
 * happy-dom never loads images; stub the global `Image` with this to report every one as loaded,
 * so an avatar renders its <img> (Radix waits for the load before it shows it).
 */
export class LoadedImage {
  complete = true;
  naturalWidth = 1;
  crossOrigin: string | null = null;
  referrerPolicy = "";
  src = "";
  addEventListener() {}
  removeEventListener() {}
}
