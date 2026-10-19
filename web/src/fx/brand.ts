// Agent-state beacons. Elements matching these get the Ember signal colours
// (styles/extras.css) and a halo that breathes (working) or beats (blocked) (vfx.ts).
// Blocked comes first: when there are more beacons than the light layer draws, the
// agents waiting for a person keep theirs.

export const beacons = {
  blocked: [".beacon.blocked"],
  working: [".beacon.working"],
};
