import { act, fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Attachment } from "@/shared/api/types.gen";
import { useLiveStore } from "@/test/live-store";
import { media, pause, play, stubMedia } from "@/test/media-element";
import { renderWithProviders } from "@/test/render";
import { AudioPlayer } from "./audio-player";

/*
 * The compact player of an audio attachment (VOI-07, VOI-08): play and pause, the time, and a seek
 * bar that is a slider.
 */

const voice: Attachment = {
  id: "att-1",
  name: "voice-message-2026-10-24-140509.webm",
  mime: "audio/webm;codecs=opus",
  size: 41_230,
  status: "stored",
};

let restoreMedia: () => void;
beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  restoreMedia = stubMedia();
});
afterEach(() => {
  restoreMedia();
  vi.unstubAllGlobals();
});

function renderPlayer(attachment: Attachment = voice) {
  const utils = renderWithProviders(<AudioPlayer attachment={attachment} />);
  const audio = utils.container.querySelector("audio") as HTMLAudioElement;
  return { ...utils, audio };
}

describe("the audio player (VOI-07)", () => {
  it("plays the stored file, loading no more than its head until it is played", () => {
    const { audio } = renderPlayer();
    expect(audio).toHaveAttribute("src", "/api/v1/attachments/att-1");
    expect(audio).toHaveAttribute("preload", "metadata");
    expect(play).not.toHaveBeenCalled();
  });

  it("is a group named for what it is: a voice message, or the file's own name", () => {
    renderPlayer();
    expect(screen.getByRole("group", { name: "Voice message" })).toBeInTheDocument();
  });

  it("is named after the file when the audio is not a recording of the app", () => {
    renderPlayer({ ...voice, name: "holiday.mp3", mime: "audio/mpeg" });
    expect(screen.getByRole("group", { name: "holiday.mp3" })).toBeInTheDocument();
  });

  it("shows the length as soon as the file says it, and 0:00 until it does", () => {
    const { audio } = renderPlayer();
    expect(screen.getByText("0:00")).toBeInTheDocument();
    expect(screen.getByRole("slider", { name: "Seek" })).toBeDisabled();

    act(() => media.load(audio, 34.7));
    expect(screen.getByText("0:00 / 0:34")).toBeInTheDocument();
    const seek = screen.getByRole("slider", { name: "Seek" });
    expect(seek).toBeEnabled();
    expect(seek).toHaveAttribute("max", "34.7");
  });

  it("plays and pauses from one button that says which it will do", async () => {
    const user = userEvent.setup();
    const { audio } = renderPlayer();
    act(() => media.load(audio, 34));

    await user.click(screen.getByRole("button", { name: "Play" }));
    expect(play).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: "Play" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "Pause" }));
    expect(pause).toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Play" })).toBeInTheDocument();
  });

  it("follows the time that passes, in the slider and in words", () => {
    const { audio } = renderPlayer();
    act(() => media.load(audio, 34));
    act(() => media.tick(audio, 12.4));
    expect(screen.getByText("0:12 / 0:34")).toBeInTheDocument();
    const seek = screen.getByRole("slider", { name: "Seek" });
    expect(seek).toHaveValue("12.4");
    expect(seek).toHaveAttribute("aria-valuetext", "0:12 of 0:34");
  });

  it("goes where the slider is moved", () => {
    const { audio } = renderPlayer();
    act(() => media.load(audio, 34));
    fireEvent.change(screen.getByRole("slider", { name: "Seek" }), { target: { value: "20" } });
    expect(media.position(audio)).toBe(20);
    expect(screen.getByText("0:20 / 0:34")).toBeInTheDocument();
  });

  it("steps a second at a time from the keyboard, as any slider does", () => {
    renderPlayer();
    expect(screen.getByRole("slider", { name: "Seek" })).toHaveAttribute("step", "1");
  });

  it("returns to the start, ready to play again, when it ends", async () => {
    const user = userEvent.setup();
    const { audio } = renderPlayer();
    act(() => media.load(audio, 34));
    await user.click(screen.getByRole("button", { name: "Play" }));
    act(() => media.tick(audio, 33.9));
    act(() => media.end(audio));
    expect(screen.getByRole("button", { name: "Play" })).toBeInTheDocument();
    expect(media.position(audio)).toBe(0);
    expect(screen.getByText("0:00 / 0:34")).toBeInTheDocument();
  });

  it("lets only one play at a time: starting one pauses the other", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <>
        <AudioPlayer attachment={{ ...voice, id: "one", name: "one.mp3", mime: "audio/mpeg" }} />
        <AudioPlayer attachment={{ ...voice, id: "two", name: "two.mp3", mime: "audio/mpeg" }} />
      </>,
    );
    const [one, two] = Array.from(document.querySelectorAll("audio"));
    const first = player("one.mp3");
    await user.click(first.getByRole("button", { name: "Play" }));
    expect(media.paused(one as HTMLAudioElement)).toBe(false);

    await user.click(player("two.mp3").getByRole("button", { name: "Play" }));
    expect(media.paused(two as HTMLAudioElement)).toBe(false);
    expect(media.paused(one as HTMLAudioElement)).toBe(true);
    expect(first.getByRole("button", { name: "Play" })).toBeInTheDocument();
  });

  it("stops playing when it leaves the page", async () => {
    const user = userEvent.setup();
    const { audio, unmount } = renderPlayer();
    await user.click(screen.getByRole("button", { name: "Play" }));
    unmount();
    expect(media.paused(audio)).toBe(true);
  });

  it("is in Portuguese when the app is", () => {
    useLiveStore.setState({ locale: "pt-BR" });
    renderPlayer();
    expect(screen.getByRole("group", { name: "Mensagem de voz" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reproduzir" })).toBeInTheDocument();
    expect(screen.getByRole("slider", { name: "Posição" })).toBeInTheDocument();
  });
});

describe("a recording that does not know its own length (VOI-08)", () => {
  it("asks for its end to learn it, shows it, and goes back to the start", () => {
    const { audio } = renderPlayer();
    // MediaRecorder writes no length into a WebM: the element reports Infinity.
    act(() => media.load(audio, Number.POSITIVE_INFINITY));
    expect(media.seeks(audio)).toHaveLength(1);
    expect(media.seeks(audio)[0]).toBeGreaterThan(1e9);
    // Not a length yet: the slider waits.
    expect(screen.getByRole("slider", { name: "Seek" })).toBeDisabled();

    // Having gone to the end, the element knows how long it is.
    act(() => media.load(audio, 12.6));
    expect(media.seeks(audio)).toHaveLength(2);
    expect(media.seeks(audio)[1]).toBe(0);
    expect(screen.getByText("0:00 / 0:12")).toBeInTheDocument();
    expect(screen.getByRole("slider", { name: "Seek" })).toBeEnabled();
  });

  it("does not ask twice, and does not mistake the trip to the end for playback", () => {
    const { audio } = renderPlayer();
    act(() => media.load(audio, Number.POSITIVE_INFINITY));
    act(() => media.load(audio, Number.POSITIVE_INFINITY));
    expect(media.seeks(audio)).toHaveLength(1);
    // The trip itself fires timeupdate: the position shown stays where it was.
    expect(screen.queryByText(/0:[1-9]\d/)).toBeNull();
  });

  it("keeps working when the length never comes: it plays, and shows the time it has", async () => {
    const user = userEvent.setup();
    const { audio } = renderPlayer();
    act(() => media.load(audio, Number.POSITIVE_INFINITY));
    await user.click(screen.getByRole("button", { name: "Play" }));
    act(() => media.tick(audio, 3));
    expect(screen.getByText("0:03")).toBeInTheDocument();
  });
});

describe("a file that cannot be played here (VOI-07)", () => {
  it("says so and offers the file to download instead", () => {
    const { audio } = renderPlayer();
    act(() => media.fail(audio));
    expect(screen.getByText("This audio can’t be played here.")).toBeInTheDocument();
    const link = screen.getByRole("link", { name: /voice-message-2026-10-24-140509\.webm/ });
    expect(link).toHaveAttribute("href", "/api/v1/attachments/att-1");
    expect(link).toHaveAttribute("download", voice.name);
    expect(screen.queryByRole("slider")).toBeNull();
    expect(screen.queryByRole("button", { name: "Play" })).toBeNull();
  });
});

/** The controls of one player, found by the name of its group (a file name). */
const player = (name: string) => within(screen.getByRole("group", { name }));
