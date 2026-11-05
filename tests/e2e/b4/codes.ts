// Business-code registry for B4 specs (dependency-free so node --test can load
// it). Every B4 test that writes data derives its codes from a label; two
// different tests must never derive the same code within one B4_RUN_ID, because
// asset/equipment codes are unique per project and the second create would fail
// (or, worse, silently reuse another test's record).

export type CodeRegistry = {
  /** Record that `owner` (a test title, or the spec file for module-level codes) uses `value`. Throws if another owner already did. */
  claim(value: string, owner: string): string;
};

export function createCodeRegistry(): CodeRegistry {
  const owners = new Map<string, string>();
  return {
    claim(value, owner) {
      const previous = owners.get(value);
      if (previous !== undefined && previous !== owner) {
        throw new Error(`B4 business code ${value} is used by two tests: "${previous}" and "${owner}" — give each test its own label`);
      }
      owners.set(value, owner);
      return value;
    },
  };
}
