import { describe, expect, it } from "vitest";
import { containerStateLabel, containerVariant } from "./state";

/*
 * What a container's state looks like (CA-06): a restart loop is not a running container, so its dot
 * is the destructive one and the word beside it says Restarting.
 */

describe("the state of a container (CA-06)", () => {
  it("draws a restart loop as an error, never as online", () => {
    expect(containerVariant("restarting")).toBe("error");
    expect(containerVariant("running")).toBe("online");
    expect(containerVariant("stopped")).toBe("stopped");
    expect(containerVariant("error")).toBe("error");
    expect(containerVariant("unknown")).toBe("connecting");
    expect(containerStateLabel("restarting")).toBe("container.state.restarting");
  });
});
