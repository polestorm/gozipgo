// Package format holds the constants of the ZIP container: record signatures, sizes of the fixed parts, bits of
// the general purpose flag, compression methods and extra field identifiers. It has no logic.
//
// Source: APPNOTE.TXT 6.3.10 (docs/provenance.md). Section numbers in the comments refer to that text.
package format

// Sygnatury rekordów (APPNOTE 4.3.7–4.3.16, 8.5.3–8.5.4). Wartości jako liczby 32-bitowe w zapisie little-endian.
const (
	SigLocalHeader      = 0x04034b50 // 4.3.7
	SigDataDescriptor   = 0x08074b50 // 4.3.9.3; także znacznik archiwum dzielonego (8.5.3)
	SigArchiveExtraData = 0x08064b50 // 4.3.11
	SigCentralHeader    = 0x02014b50 // 4.3.12
	SigDigitalSignature = 0x05054b50 // 4.3.13
	SigZip64End         = 0x06064b50 // 4.3.14
	SigZip64Locator     = 0x07064b50 // 4.3.15
	SigEnd              = 0x06054b50 // 4.3.16
	SigSpanningMarker   = 0x30304b50 // 8.5.4
)

// Rozmiary części stałych rekordów, w bajtach.
const (
	LocalHeaderSize   = 30 // 4.3.7
	CentralHeaderSize = 46 // 4.3.12
	EndSize           = 22 // 4.3.16
	Zip64LocatorSize  = 20 // 4.3.15
	Zip64EndSize      = 56 // 4.3.14, bez sektora danych rozszerzalnych
	// Zip64EndSizeField to wartość pola „size of zip64 end of central directory record” dla rekordu w wersji 1
	// bez sektora danych: rozmiar rekordu bez pierwszych 12 bajtów (4.3.14.1).
	Zip64EndSizeField = Zip64EndSize - 12
	MaxCommentLength  = 0xFFFF
)

// Wartości graniczne odsyłające do rekordów ZIP64 (4.4.1.4).
const (
	Sentinel16 = 0xFFFF
	Sentinel32 = 0xFFFFFFFF
)

// Bity flagi ogólnej (4.4.4).
const (
	FlagEncrypted        = 1 << 0
	FlagMethodOption1    = 1 << 1
	FlagMethodOption2    = 1 << 2
	FlagDataDescriptor   = 1 << 3
	FlagEnhancedDeflate  = 1 << 4
	FlagPatchData        = 1 << 5
	FlagStrongEncryption = 1 << 6
	FlagUTF8             = 1 << 11
	FlagMaskedHeaders    = 1 << 13

	// FlagsReserved: bit 4 (zarezerwowany dla „enhanced deflating”), bity 7–10 (nieużywane), 12, 14 i 15
	// (zarezerwowane przez PKWARE).
	FlagsReserved = 1<<4 | 1<<7 | 1<<8 | 1<<9 | 1<<10 | 1<<12 | 1<<14 | 1<<15
)

// Metody kompresji w zakresie (4.4.5).
const (
	MethodStored  = 0
	MethodDeflate = 8
)

// VersionZip64 to najwyższa obsługiwana wartość „version needed to extract”: 4.5 (4.4.3.2).
const VersionZip64 = 45

// Identyfikatory pól dodatkowych, które czytnik rozumie albo rozpoznaje (4.5.2, 4.6.1).
const (
	ExtraZip64            = 0x0001 // 4.5.3
	ExtraPatchDescriptor  = 0x000f // 4.5.8
	ExtraStrongEncryption = 0x0017 // 4.5.12
	ExtraUnicodePath      = 0x7075 // 4.6.9
	ExtraAEx              = 0x9901 // dodatek E
)
