/*
 * The rules of an owner's secret, mirrored from `internal/ownersecrets` (the API generated no
 * constants for them): the name becomes an environment variable of a container (CA-03), and the
 * value is 1 to 16384 bytes, counted in UTF-8 bytes like the server counts it.
 */

/** A name the server accepts: `^[A-Za-z][A-Za-z0-9_-]{0,63}$`. */
export const SecretNamePattern = "^[A-Za-z][A-Za-z0-9_-]{0,63}$";

/** The longest value the server stores. */
export const MaxSecretValueBytes = 16384;

export const secretNameRe = new RegExp(SecretNamePattern);

/** The value's length as the server counts it: bytes, not characters. */
export const secretValueLength = (value: string): number => new TextEncoder().encode(value).length;

/** The most a value may be, for the input's own check. */
export const secretValueTooLong = (value: string): boolean =>
  secretValueLength(value) > MaxSecretValueBytes;
