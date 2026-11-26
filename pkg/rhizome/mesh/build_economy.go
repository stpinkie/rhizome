//go:build economy

package mesh

// economyBuild is true only in economy-tagged builds. Bucket-B (cross-user
// economy) code paths check it so the default build compiles the same
// sources but never activates settlement, billing, or market behavior.
const economyBuild = true
