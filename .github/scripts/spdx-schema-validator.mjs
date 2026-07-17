// Generated from SPDX v2.3 official schema (tag v2.3), upstream sha256:239208b7ac287b3cf5d9a9af23f9d69863971102a5e1587a27a398b43490b89b.
"use strict";
export const validate = validate10;
export default validate10;
const schema11 = {"$schema":"http://json-schema.org/draft-07/schema#","$id":"http://spdx.org/rdf/terms/2.3","title":"SPDX 2.3","type":"object","properties":{"SPDXID":{"type":"string","description":"Uniquely identify any element in an SPDX document which may be referenced by other elements."},"annotations":{"description":"Provide additional information about an SpdxElement.","type":"array","items":{"type":"object","properties":{"annotationDate":{"description":"Identify when the comment was made. This is to be specified according to the combined date and time in the UTC format, as specified in the ISO 8601 standard.","type":"string"},"annotationType":{"description":"Type of the annotation.","type":"string","enum":["OTHER","REVIEW"]},"annotator":{"description":"This field identifies the person, organization, or tool that has commented on a file, package, snippet, or the entire document.","type":"string"},"comment":{"type":"string"}},"required":["annotationDate","annotationType","annotator","comment"],"additionalProperties":false,"description":"An Annotation is a comment on an SpdxItem by an agent."}},"comment":{"type":"string"},"creationInfo":{"type":"object","properties":{"comment":{"type":"string"},"created":{"description":"Identify when the SPDX document was originally created. The date is to be specified according to combined date and time in UTC format as specified in ISO 8601 standard.","type":"string"},"creators":{"description":"Identify who (or what, in the case of a tool) created the SPDX document. If the SPDX document was created by an individual, indicate the person's name. If the SPDX document was created on behalf of a company or organization, indicate the entity name. If the SPDX document was created using a software tool, indicate the name and version for that tool. If multiple participants or tools were involved, use multiple instances of this field. Person name or organization name may be designated as “anonymous” if appropriate.","minItems":1,"type":"array","items":{"description":"Identify who (or what, in the case of a tool) created the SPDX document. If the SPDX document was created by an individual, indicate the person's name. If the SPDX document was created on behalf of a company or organization, indicate the entity name. If the SPDX document was created using a software tool, indicate the name and version for that tool. If multiple participants or tools were involved, use multiple instances of this field. Person name or organization name may be designated as “anonymous” if appropriate.","type":"string"}},"licenseListVersion":{"description":"An optional field for creators of the SPDX file to provide the version of the SPDX License List used when the SPDX file was created.","type":"string"}},"required":["created","creators"],"additionalProperties":false,"description":"One instance is required for each SPDX file produced. It provides the necessary information for forward and backward compatibility for processing tools."},"dataLicense":{"description":"License expression for dataLicense. See SPDX Annex D for the license expression syntax.  Compliance with the SPDX specification includes populating the SPDX fields therein with data related to such fields (\"SPDX-Metadata\"). The SPDX specification contains numerous fields where an SPDX document creator may provide relevant explanatory text in SPDX-Metadata. Without opining on the lawfulness of \"database rights\" (in jurisdictions where applicable), such explanatory text is copyrightable subject matter in most Berne Convention countries. By using the SPDX specification, or any portion hereof, you hereby agree that any copyright rights (as determined by your jurisdiction) in any SPDX-Metadata, including without limitation explanatory text, shall be subject to the terms of the Creative Commons CC0 1.0 Universal license. For SPDX-Metadata not containing any copyright rights, you hereby agree and acknowledge that the SPDX-Metadata is provided to you \"as-is\" and without any representations or warranties of any kind concerning the SPDX-Metadata, express, implied, statutory or otherwise, including without limitation warranties of title, merchantability, fitness for a particular purpose, non-infringement, or the absence of latent or other defects, accuracy, or the presence or absence of errors, whether or not discoverable, all to the greatest extent permissible under applicable law.","type":"string"},"externalDocumentRefs":{"description":"Identify any external SPDX documents referenced within this SPDX document.","type":"array","items":{"type":"object","properties":{"checksum":{"type":"object","properties":{"algorithm":{"description":"Identifies the algorithm used to produce the subject Checksum. Currently, SHA-1 is the only supported algorithm. It is anticipated that other algorithms will be supported at a later time.","type":"string","enum":["SHA1","BLAKE3","SHA3-384","SHA256","SHA384","BLAKE2b-512","BLAKE2b-256","SHA3-512","MD2","ADLER32","MD4","SHA3-256","BLAKE2b-384","SHA512","MD6","MD5","SHA224"]},"checksumValue":{"description":"The checksumValue property provides a lower case hexidecimal encoded digest value produced using a specific algorithm.","type":"string"}},"required":["algorithm","checksumValue"],"additionalProperties":false,"description":"A Checksum is value that allows the contents of a file to be authenticated. Even small changes to the content of the file will change its checksum. This class allows the results of a variety of checksum and cryptographic message digest algorithms to be represented."},"externalDocumentId":{"description":"externalDocumentId is a string containing letters, numbers, ., - and/or + which uniquely identifies an external document within this document.","type":"string"},"spdxDocument":{"description":"SPDX ID for SpdxDocument.  A property containing an SPDX document.","type":"string"}},"required":["checksum","externalDocumentId","spdxDocument"],"additionalProperties":false,"description":"Information about an external SPDX document reference including the checksum. This allows for verification of the external references."}},"hasExtractedLicensingInfos":{"description":"Indicates that a particular ExtractedLicensingInfo was defined in the subject SpdxDocument.","type":"array","items":{"type":"object","properties":{"comment":{"type":"string"},"crossRefs":{"description":"Cross Reference Detail for a license SeeAlso URL","type":"array","items":{"type":"object","properties":{"isLive":{"description":"Indicate a URL is still a live accessible location on the public internet","type":"boolean"},"isValid":{"description":"True if the URL is a valid well formed URL","type":"boolean"},"isWayBackLink":{"description":"True if the License SeeAlso URL points to a Wayback archive","type":"boolean"},"match":{"description":"Status of a License List SeeAlso URL reference if it refers to a website that matches the license text.","type":"string"},"order":{"description":"The ordinal order of this element within a list","type":"integer"},"timestamp":{"description":"Timestamp","type":"string"},"url":{"description":"URL Reference","type":"string"}},"required":["url"],"additionalProperties":false,"description":"Cross reference details for the a URL reference"}},"extractedText":{"description":"Provide a copy of the actual text of the license reference extracted from the package, file or snippet that is associated with the License Identifier to aid in future analysis.","type":"string"},"licenseId":{"description":"A human readable short form license identifier for a license. The license ID is either on the standard license list or the form \"LicenseRef-[idString]\" where [idString] is a unique string containing letters, numbers, \".\" or \"-\".  When used within a license expression, the license ID can optionally include a reference to an external document in the form \"DocumentRef-[docrefIdString]:LicenseRef-[idString]\" where docRefIdString is an ID for an external document reference.","type":"string"},"name":{"description":"Identify name of this SpdxElement.","type":"string"},"seeAlsos":{"type":"array","items":{"type":"string"}}},"required":["extractedText","licenseId"],"additionalProperties":false,"description":"An ExtractedLicensingInfo represents a license or licensing notice that was found in a package, file or snippet. Any license text that is recognized as a license may be represented as a License rather than an ExtractedLicensingInfo."}},"name":{"description":"Identify name of this SpdxElement.","type":"string"},"revieweds":{"description":"Reviewed","type":"array","items":{"type":"object","properties":{"comment":{"type":"string"},"reviewDate":{"description":"The date and time at which the SpdxDocument was reviewed. This value must be in UTC and have 'Z' as its timezone indicator.","type":"string"},"reviewer":{"description":"The name and, optionally, contact information of the person who performed the review. Values of this property must conform to the agent and tool syntax.  The reviewer property is deprecated in favor of Annotation with an annotationType review.","type":"string"}},"required":["reviewDate"],"additionalProperties":false,"description":"This class has been deprecated in favor of an Annotation with an Annotation type of review."}},"spdxVersion":{"description":"Provide a reference number that can be used to understand how to parse and interpret the rest of the file. It will enable both future changes to the specification and to support backward compatibility. The version number consists of a major and minor version indicator. The major field will be incremented when incompatible changes between versions are made (one or more sections are created, modified or deleted). The minor field will be incremented when backwards compatible changes are made.","type":"string"},"documentNamespace":{"type":"string","description":"The URI provides an unambiguous mechanism for other SPDX documents to reference SPDX elements within this SPDX document."},"documentDescribes":{"description":"Packages, files and/or Snippets described by this SPDX document","type":"array","items":{"type":"string","description":"SPDX ID for each Package, File, or Snippet."}},"packages":{"description":"Packages referenced in the SPDX document","type":"array","items":{"type":"object","properties":{"SPDXID":{"type":"string","description":"Uniquely identify any element in an SPDX document which may be referenced by other elements."},"annotations":{"description":"Provide additional information about an SpdxElement.","type":"array","items":{"type":"object","properties":{"annotationDate":{"description":"Identify when the comment was made. This is to be specified according to the combined date and time in the UTC format, as specified in the ISO 8601 standard.","type":"string"},"annotationType":{"description":"Type of the annotation.","type":"string","enum":["OTHER","REVIEW"]},"annotator":{"description":"This field identifies the person, organization, or tool that has commented on a file, package, snippet, or the entire document.","type":"string"},"comment":{"type":"string"}},"required":["annotationDate","annotationType","annotator","comment"],"additionalProperties":false,"description":"An Annotation is a comment on an SpdxItem by an agent."}},"attributionTexts":{"description":"This field provides a place for the SPDX data creator to record acknowledgements that may be required to be communicated in some contexts. This is not meant to include the actual complete license text (see licenseConculded and licenseDeclared), and may or may not include copyright notices (see also copyrightText). The SPDX data creator may use this field to record other acknowledgements, such as particular clauses from license texts, which may be necessary or desirable to reproduce.","type":"array","items":{"description":"This field provides a place for the SPDX data creator to record acknowledgements that may be required to be communicated in some contexts. This is not meant to include the actual complete license text (see licenseConculded and licenseDeclared), and may or may not include copyright notices (see also copyrightText). The SPDX data creator may use this field to record other acknowledgements, such as particular clauses from license texts, which may be necessary or desirable to reproduce.","type":"string"}},"builtDate":{"description":"This field provides a place for recording the actual date the package was built.","type":"string"},"checksums":{"description":"The checksum property provides a mechanism that can be used to verify that the contents of a File or Package have not changed.","type":"array","items":{"type":"object","properties":{"algorithm":{"description":"Identifies the algorithm used to produce the subject Checksum. Currently, SHA-1 is the only supported algorithm. It is anticipated that other algorithms will be supported at a later time.","type":"string","enum":["SHA1","BLAKE3","SHA3-384","SHA256","SHA384","BLAKE2b-512","BLAKE2b-256","SHA3-512","MD2","ADLER32","MD4","SHA3-256","BLAKE2b-384","SHA512","MD6","MD5","SHA224"]},"checksumValue":{"description":"The checksumValue property provides a lower case hexidecimal encoded digest value produced using a specific algorithm.","type":"string"}},"required":["algorithm","checksumValue"],"additionalProperties":false,"description":"A Checksum is value that allows the contents of a file to be authenticated. Even small changes to the content of the file will change its checksum. This class allows the results of a variety of checksum and cryptographic message digest algorithms to be represented."}},"comment":{"type":"string"},"copyrightText":{"description":"The text of copyright declarations recited in the package, file or snippet.\n\nIf the copyrightText field is not present, it implies an equivalent meaning to NOASSERTION.","type":"string"},"description":{"description":"Provides a detailed description of the package.","type":"string"},"downloadLocation":{"description":"The URI at which this package is available for download. Private (i.e., not publicly reachable) URIs are acceptable as values of this property. The values http://spdx.org/rdf/terms#none and http://spdx.org/rdf/terms#noassertion may be used to specify that the package is not downloadable or that no attempt was made to determine its download location, respectively.","type":"string"},"externalRefs":{"description":"An External Reference allows a Package to reference an external source of additional information, metadata, enumerations, asset identifiers, or downloadable content believed to be relevant to the Package.","type":"array","items":{"type":"object","properties":{"comment":{"type":"string"},"referenceCategory":{"description":"Category for the external reference","type":"string","enum":["OTHER","PERSISTENT-ID","SECURITY","PACKAGE-MANAGER"]},"referenceLocator":{"description":"The unique string with no spaces necessary to access the package-specific information, metadata, or content within the target location. The format of the locator is subject to constraints defined by the <type>.","type":"string"},"referenceType":{"description":"Type of the external reference. These are definined in an appendix in the SPDX specification.","type":"string"}},"required":["referenceCategory","referenceLocator","referenceType"],"additionalProperties":false,"description":"An External Reference allows a Package to reference an external source of additional information, metadata, enumerations, asset identifiers, or downloadable content believed to be relevant to the Package."}},"filesAnalyzed":{"description":"Indicates whether the file content of this package has been available for or subjected to analysis when creating the SPDX document. If false indicates packages that represent metadata or URI references to a project, product, artifact, distribution or a component. If set to false, the package must not contain any files.","type":"boolean"},"hasFiles":{"description":"Indicates that a particular file belongs to a package.","type":"array","items":{"description":"SPDX ID for File.  Indicates that a particular file belongs to a package.","type":"string"}},"homepage":{"type":"string"},"licenseComments":{"description":"The licenseComments property allows the preparer of the SPDX document to describe why the licensing in spdx:licenseConcluded was chosen.","type":"string"},"licenseConcluded":{"description":"License expression for licenseConcluded. See SPDX Annex D for the license expression syntax.  The licensing that the preparer of this SPDX document has concluded, based on the evidence, actually applies to the SPDX Item.\n\nIf the licenseConcluded field is not present for an SPDX Item, it implies an equivalent meaning to NOASSERTION.","type":"string"},"licenseDeclared":{"description":"License expression for licenseDeclared. See SPDX Annex D for the license expression syntax.  The licensing that the creators of the software in the package, or the packager, have declared. Declarations by the original software creator should be preferred, if they exist.","type":"string"},"licenseInfoFromFiles":{"description":"The licensing information that was discovered directly within the package. There will be an instance of this property for each distinct value of alllicenseInfoInFile properties of all files contained in the package.\n\nIf the licenseInfoFromFiles field is not present for a package and filesAnalyzed property for that same pacakge is true or omitted, it implies an equivalent meaning to NOASSERTION.","type":"array","items":{"description":"License expression for licenseInfoFromFiles. See SPDX Annex D for the license expression syntax.  The licensing information that was discovered directly within the package. There will be an instance of this property for each distinct value of alllicenseInfoInFile properties of all files contained in the package.\n\nIf the licenseInfoFromFiles field is not present for a package and filesAnalyzed property for that same pacakge is true or omitted, it implies an equivalent meaning to NOASSERTION.","type":"string"}},"name":{"description":"Identify name of this SpdxElement.","type":"string"},"originator":{"description":"The name and, optionally, contact information of the person or organization that originally created the package. Values of this property must conform to the agent and tool syntax.","type":"string"},"packageFileName":{"description":"The base name of the package file name. For example, zlib-1.2.5.tar.gz.","type":"string"},"packageVerificationCode":{"type":"object","properties":{"packageVerificationCodeExcludedFiles":{"description":"A file that was excluded when calculating the package verification code. This is usually a file containing SPDX data regarding the package. If a package contains more than one SPDX file all SPDX files must be excluded from the package verification code. If this is not done it would be impossible to correctly calculate the verification codes in both files.","type":"array","items":{"description":"A file that was excluded when calculating the package verification code. This is usually a file containing SPDX data regarding the package. If a package contains more than one SPDX file all SPDX files must be excluded from the package verification code. If this is not done it would be impossible to correctly calculate the verification codes in both files.","type":"string"}},"packageVerificationCodeValue":{"description":"The actual package verification code as a hex encoded value.","type":"string"}},"required":["packageVerificationCodeValue"],"additionalProperties":false,"description":"A manifest based verification code (the algorithm is defined in section 4.7 of the full specification) of the SPDX Item. This allows consumers of this data and/or database to determine if an SPDX item they have in hand is identical to the SPDX item from which the data was produced. This algorithm works even if the SPDX document is included in the SPDX item."},"primaryPackagePurpose":{"description":"This field provides information about the primary purpose of the identified package. Package Purpose is intrinsic to how the package is being used rather than the content of the package.","type":"string","enum":["OTHER","INSTALL","ARCHIVE","FIRMWARE","APPLICATION","FRAMEWORK","LIBRARY","CONTAINER","SOURCE","DEVICE","OPERATING_SYSTEM","FILE"]},"releaseDate":{"description":"This field provides a place for recording the date the package was released.","type":"string"},"sourceInfo":{"description":"Allows the producer(s) of the SPDX document to describe how the package was acquired and/or changed from the original source.","type":"string"},"summary":{"description":"Provides a short description of the package.","type":"string"},"supplier":{"description":"The name and, optionally, contact information of the person or organization who was the immediate supplier of this package to the recipient. The supplier may be different than originator when the software has been repackaged. Values of this property must conform to the agent and tool syntax.","type":"string"},"validUntilDate":{"description":"This field provides a place for recording the end of the support period for a package from the supplier.","type":"string"},"versionInfo":{"description":"Provides an indication of the version of the package that is described by this SpdxDocument.","type":"string"}},"required":["SPDXID","downloadLocation","name"],"additionalProperties":false}},"files":{"description":"Files referenced in the SPDX document","type":"array","items":{"type":"object","properties":{"SPDXID":{"type":"string","description":"Uniquely identify any element in an SPDX document which may be referenced by other elements."},"annotations":{"description":"Provide additional information about an SpdxElement.","type":"array","items":{"type":"object","properties":{"annotationDate":{"description":"Identify when the comment was made. This is to be specified according to the combined date and time in the UTC format, as specified in the ISO 8601 standard.","type":"string"},"annotationType":{"description":"Type of the annotation.","type":"string","enum":["OTHER","REVIEW"]},"annotator":{"description":"This field identifies the person, organization, or tool that has commented on a file, package, snippet, or the entire document.","type":"string"},"comment":{"type":"string"}},"required":["annotationDate","annotationType","annotator","comment"],"additionalProperties":false,"description":"An Annotation is a comment on an SpdxItem by an agent."}},"artifactOfs":{"description":"Indicates the project in which the SpdxElement originated. Tools must preserve doap:homepage and doap:name properties and the URI (if one is known) of doap:Project resources that are values of this property. All other properties of doap:Projects are not directly supported by SPDX and may be dropped when translating to or from some SPDX formats.","type":"array","items":{"type":"object"}},"attributionTexts":{"description":"This field provides a place for the SPDX data creator to record acknowledgements that may be required to be communicated in some contexts. This is not meant to include the actual complete license text (see licenseConculded and licenseDeclared), and may or may not include copyright notices (see also copyrightText). The SPDX data creator may use this field to record other acknowledgements, such as particular clauses from license texts, which may be necessary or desirable to reproduce.","type":"array","items":{"description":"This field provides a place for the SPDX data creator to record acknowledgements that may be required to be communicated in some contexts. This is not meant to include the actual complete license text (see licenseConculded and licenseDeclared), and may or may not include copyright notices (see also copyrightText). The SPDX data creator may use this field to record other acknowledgements, such as particular clauses from license texts, which may be necessary or desirable to reproduce.","type":"string"}},"checksums":{"description":"The checksum property provides a mechanism that can be used to verify that the contents of a File or Package have not changed.","minItems":1,"type":"array","items":{"type":"object","properties":{"algorithm":{"description":"Identifies the algorithm used to produce the subject Checksum. Currently, SHA-1 is the only supported algorithm. It is anticipated that other algorithms will be supported at a later time.","type":"string","enum":["SHA1","BLAKE3","SHA3-384","SHA256","SHA384","BLAKE2b-512","BLAKE2b-256","SHA3-512","MD2","ADLER32","MD4","SHA3-256","BLAKE2b-384","SHA512","MD6","MD5","SHA224"]},"checksumValue":{"description":"The checksumValue property provides a lower case hexidecimal encoded digest value produced using a specific algorithm.","type":"string"}},"required":["algorithm","checksumValue"],"additionalProperties":false,"description":"A Checksum is value that allows the contents of a file to be authenticated. Even small changes to the content of the file will change its checksum. This class allows the results of a variety of checksum and cryptographic message digest algorithms to be represented."}},"comment":{"type":"string"},"copyrightText":{"description":"The text of copyright declarations recited in the package, file or snippet.\n\nIf the copyrightText field is not present, it implies an equivalent meaning to NOASSERTION.","type":"string"},"fileContributors":{"description":"This field provides a place for the SPDX file creator to record file contributors. Contributors could include names of copyright holders and/or authors who may not be copyright holders yet contributed to the file content.","type":"array","items":{"description":"This field provides a place for the SPDX file creator to record file contributors. Contributors could include names of copyright holders and/or authors who may not be copyright holders yet contributed to the file content.","type":"string"}},"fileDependencies":{"description":"This field is deprecated since SPDX 2.0 in favor of using Section 7 which provides more granularity about relationships.","type":"array","items":{"description":"SPDX ID for File.  This field is deprecated since SPDX 2.0 in favor of using Section 7 which provides more granularity about relationships.","type":"string"}},"fileName":{"description":"The name of the file relative to the root of the package.","type":"string"},"fileTypes":{"description":"The type of the file.","type":"array","items":{"description":"The type of the file.","type":"string","enum":["OTHER","DOCUMENTATION","IMAGE","VIDEO","ARCHIVE","SPDX","APPLICATION","SOURCE","BINARY","TEXT","AUDIO"]}},"licenseComments":{"description":"The licenseComments property allows the preparer of the SPDX document to describe why the licensing in spdx:licenseConcluded was chosen.","type":"string"},"licenseConcluded":{"description":"License expression for licenseConcluded. See SPDX Annex D for the license expression syntax.  The licensing that the preparer of this SPDX document has concluded, based on the evidence, actually applies to the SPDX Item.\n\nIf the licenseConcluded field is not present for an SPDX Item, it implies an equivalent meaning to NOASSERTION.","type":"string"},"licenseInfoInFiles":{"description":"Licensing information that was discovered directly in the subject file. This is also considered a declared license for the file.\n\nIf the licenseInfoInFile field is not present for a file, it implies an equivalent meaning to NOASSERTION.","type":"array","items":{"description":"License expression for licenseInfoInFile. See SPDX Annex D for the license expression syntax.  Licensing information that was discovered directly in the subject file. This is also considered a declared license for the file.\n\nIf the licenseInfoInFile field is not present for a file, it implies an equivalent meaning to NOASSERTION.","type":"string"}},"noticeText":{"description":"This field provides a place for the SPDX file creator to record potential legal notices found in the file. This may or may not include copyright statements.","type":"string"}},"required":["SPDXID","checksums","fileName"],"additionalProperties":false}},"snippets":{"description":"Snippets referenced in the SPDX document","type":"array","items":{"type":"object","properties":{"SPDXID":{"type":"string","description":"Uniquely identify any element in an SPDX document which may be referenced by other elements."},"annotations":{"description":"Provide additional information about an SpdxElement.","type":"array","items":{"type":"object","properties":{"annotationDate":{"description":"Identify when the comment was made. This is to be specified according to the combined date and time in the UTC format, as specified in the ISO 8601 standard.","type":"string"},"annotationType":{"description":"Type of the annotation.","type":"string","enum":["OTHER","REVIEW"]},"annotator":{"description":"This field identifies the person, organization, or tool that has commented on a file, package, snippet, or the entire document.","type":"string"},"comment":{"type":"string"}},"required":["annotationDate","annotationType","annotator","comment"],"additionalProperties":false,"description":"An Annotation is a comment on an SpdxItem by an agent."}},"attributionTexts":{"description":"This field provides a place for the SPDX data creator to record acknowledgements that may be required to be communicated in some contexts. This is not meant to include the actual complete license text (see licenseConculded and licenseDeclared), and may or may not include copyright notices (see also copyrightText). The SPDX data creator may use this field to record other acknowledgements, such as particular clauses from license texts, which may be necessary or desirable to reproduce.","type":"array","items":{"description":"This field provides a place for the SPDX data creator to record acknowledgements that may be required to be communicated in some contexts. This is not meant to include the actual complete license text (see licenseConculded and licenseDeclared), and may or may not include copyright notices (see also copyrightText). The SPDX data creator may use this field to record other acknowledgements, such as particular clauses from license texts, which may be necessary or desirable to reproduce.","type":"string"}},"comment":{"type":"string"},"copyrightText":{"description":"The text of copyright declarations recited in the package, file or snippet.\n\nIf the copyrightText field is not present, it implies an equivalent meaning to NOASSERTION.","type":"string"},"licenseComments":{"description":"The licenseComments property allows the preparer of the SPDX document to describe why the licensing in spdx:licenseConcluded was chosen.","type":"string"},"licenseConcluded":{"description":"License expression for licenseConcluded. See SPDX Annex D for the license expression syntax.  The licensing that the preparer of this SPDX document has concluded, based on the evidence, actually applies to the SPDX Item.\n\nIf the licenseConcluded field is not present for an SPDX Item, it implies an equivalent meaning to NOASSERTION.","type":"string"},"licenseInfoInSnippets":{"description":"Licensing information that was discovered directly in the subject snippet. This is also considered a declared license for the snippet.\n\nIf the licenseInfoInSnippet field is not present for a snippet, it implies an equivalent meaning to NOASSERTION.","type":"array","items":{"description":"License expression for licenseInfoInSnippet. See SPDX Annex D for the license expression syntax.  Licensing information that was discovered directly in the subject snippet. This is also considered a declared license for the snippet.\n\nIf the licenseInfoInSnippet field is not present for a snippet, it implies an equivalent meaning to NOASSERTION.","type":"string"}},"name":{"description":"Identify name of this SpdxElement.","type":"string"},"ranges":{"description":"This field defines the byte range in the original host file (in X.2) that the snippet information applies to","minItems":1,"type":"array","items":{"type":"object","properties":{"endPointer":{"type":"object","properties":{"reference":{"description":"SPDX ID for File","type":"string"},"offset":{"type":"integer","description":"Byte offset in the file"},"lineNumber":{"type":"integer","description":"line number offset in the file"}},"required":["reference"],"additionalProperties":false},"startPointer":{"type":"object","properties":{"reference":{"description":"SPDX ID for File","type":"string"},"offset":{"type":"integer","description":"Byte offset in the file"},"lineNumber":{"type":"integer","description":"line number offset in the file"}},"required":["reference"],"additionalProperties":false}},"required":["endPointer","startPointer"],"additionalProperties":false}},"snippetFromFile":{"description":"SPDX ID for File.  File containing the SPDX element (e.g. the file contaning a snippet).","type":"string"}},"required":["SPDXID","name","ranges","snippetFromFile"],"additionalProperties":false}},"relationships":{"description":"Relationships referenced in the SPDX document","type":"array","items":{"type":"object","properties":{"spdxElementId":{"type":"string","description":"Id to which the SPDX element is related"},"comment":{"type":"string"},"relatedSpdxElement":{"description":"SPDX ID for SpdxElement.  A related SpdxElement.","type":"string"},"relationshipType":{"description":"Describes the type of relationship between two SPDX elements.","type":"string","enum":["VARIANT_OF","COPY_OF","PATCH_FOR","TEST_DEPENDENCY_OF","CONTAINED_BY","DATA_FILE_OF","OPTIONAL_COMPONENT_OF","ANCESTOR_OF","GENERATES","CONTAINS","OPTIONAL_DEPENDENCY_OF","FILE_ADDED","REQUIREMENT_DESCRIPTION_FOR","DEV_DEPENDENCY_OF","DEPENDENCY_OF","BUILD_DEPENDENCY_OF","DESCRIBES","PREREQUISITE_FOR","HAS_PREREQUISITE","PROVIDED_DEPENDENCY_OF","DYNAMIC_LINK","DESCRIBED_BY","METAFILE_OF","DEPENDENCY_MANIFEST_OF","PATCH_APPLIED","RUNTIME_DEPENDENCY_OF","TEST_OF","TEST_TOOL_OF","DEPENDS_ON","SPECIFICATION_FOR","FILE_MODIFIED","DISTRIBUTION_ARTIFACT","AMENDS","DOCUMENTATION_OF","GENERATED_FROM","STATIC_LINK","OTHER","BUILD_TOOL_OF","TEST_CASE_OF","PACKAGE_OF","DESCENDANT_OF","FILE_DELETED","EXPANDED_FROM_ARCHIVE","DEV_TOOL_OF","EXAMPLE_OF"]}},"required":["spdxElementId","relatedSpdxElement","relationshipType"],"additionalProperties":false}}},"required":["SPDXID","creationInfo","dataLicense","name","spdxVersion"],"additionalProperties":false};
const func2 = Object.prototype.hasOwnProperty;

function validate10(data, {instancePath="", parentData, parentDataProperty, rootData=data}={}){
/*# sourceURL="http://spdx.org/rdf/terms/2.3" */;
let vErrors = null;
let errors = 0;
if(data && typeof data == "object" && !Array.isArray(data)){
if(data.SPDXID === undefined){
const err0 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "SPDXID"},message:"must have required property '"+"SPDXID"+"'"};
if(vErrors === null){
vErrors = [err0];
}
else {
vErrors.push(err0);
}
errors++;
}
if(data.creationInfo === undefined){
const err1 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "creationInfo"},message:"must have required property '"+"creationInfo"+"'"};
if(vErrors === null){
vErrors = [err1];
}
else {
vErrors.push(err1);
}
errors++;
}
if(data.dataLicense === undefined){
const err2 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "dataLicense"},message:"must have required property '"+"dataLicense"+"'"};
if(vErrors === null){
vErrors = [err2];
}
else {
vErrors.push(err2);
}
errors++;
}
if(data.name === undefined){
const err3 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "name"},message:"must have required property '"+"name"+"'"};
if(vErrors === null){
vErrors = [err3];
}
else {
vErrors.push(err3);
}
errors++;
}
if(data.spdxVersion === undefined){
const err4 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "spdxVersion"},message:"must have required property '"+"spdxVersion"+"'"};
if(vErrors === null){
vErrors = [err4];
}
else {
vErrors.push(err4);
}
errors++;
}
for(const key0 in data){
if(!(func2.call(schema11.properties, key0))){
const err5 = {instancePath,schemaPath:"#/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key0},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err5];
}
else {
vErrors.push(err5);
}
errors++;
}
}
if(data.SPDXID !== undefined){
if(typeof data.SPDXID !== "string"){
const err6 = {instancePath:instancePath+"/SPDXID",schemaPath:"#/properties/SPDXID/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err6];
}
else {
vErrors.push(err6);
}
errors++;
}
}
if(data.annotations !== undefined){
let data1 = data.annotations;
if(Array.isArray(data1)){
const len0 = data1.length;
for(let i0=0; i0<len0; i0++){
let data2 = data1[i0];
if(data2 && typeof data2 == "object" && !Array.isArray(data2)){
if(data2.annotationDate === undefined){
const err7 = {instancePath:instancePath+"/annotations/" + i0,schemaPath:"#/properties/annotations/items/required",keyword:"required",params:{missingProperty: "annotationDate"},message:"must have required property '"+"annotationDate"+"'"};
if(vErrors === null){
vErrors = [err7];
}
else {
vErrors.push(err7);
}
errors++;
}
if(data2.annotationType === undefined){
const err8 = {instancePath:instancePath+"/annotations/" + i0,schemaPath:"#/properties/annotations/items/required",keyword:"required",params:{missingProperty: "annotationType"},message:"must have required property '"+"annotationType"+"'"};
if(vErrors === null){
vErrors = [err8];
}
else {
vErrors.push(err8);
}
errors++;
}
if(data2.annotator === undefined){
const err9 = {instancePath:instancePath+"/annotations/" + i0,schemaPath:"#/properties/annotations/items/required",keyword:"required",params:{missingProperty: "annotator"},message:"must have required property '"+"annotator"+"'"};
if(vErrors === null){
vErrors = [err9];
}
else {
vErrors.push(err9);
}
errors++;
}
if(data2.comment === undefined){
const err10 = {instancePath:instancePath+"/annotations/" + i0,schemaPath:"#/properties/annotations/items/required",keyword:"required",params:{missingProperty: "comment"},message:"must have required property '"+"comment"+"'"};
if(vErrors === null){
vErrors = [err10];
}
else {
vErrors.push(err10);
}
errors++;
}
for(const key1 in data2){
if(!((((key1 === "annotationDate") || (key1 === "annotationType")) || (key1 === "annotator")) || (key1 === "comment"))){
const err11 = {instancePath:instancePath+"/annotations/" + i0,schemaPath:"#/properties/annotations/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key1},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err11];
}
else {
vErrors.push(err11);
}
errors++;
}
}
if(data2.annotationDate !== undefined){
if(typeof data2.annotationDate !== "string"){
const err12 = {instancePath:instancePath+"/annotations/" + i0+"/annotationDate",schemaPath:"#/properties/annotations/items/properties/annotationDate/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err12];
}
else {
vErrors.push(err12);
}
errors++;
}
}
if(data2.annotationType !== undefined){
let data4 = data2.annotationType;
if(typeof data4 !== "string"){
const err13 = {instancePath:instancePath+"/annotations/" + i0+"/annotationType",schemaPath:"#/properties/annotations/items/properties/annotationType/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err13];
}
else {
vErrors.push(err13);
}
errors++;
}
if(!((data4 === "OTHER") || (data4 === "REVIEW"))){
const err14 = {instancePath:instancePath+"/annotations/" + i0+"/annotationType",schemaPath:"#/properties/annotations/items/properties/annotationType/enum",keyword:"enum",params:{allowedValues: schema11.properties.annotations.items.properties.annotationType.enum},message:"must be equal to one of the allowed values"};
if(vErrors === null){
vErrors = [err14];
}
else {
vErrors.push(err14);
}
errors++;
}
}
if(data2.annotator !== undefined){
if(typeof data2.annotator !== "string"){
const err15 = {instancePath:instancePath+"/annotations/" + i0+"/annotator",schemaPath:"#/properties/annotations/items/properties/annotator/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err15];
}
else {
vErrors.push(err15);
}
errors++;
}
}
if(data2.comment !== undefined){
if(typeof data2.comment !== "string"){
const err16 = {instancePath:instancePath+"/annotations/" + i0+"/comment",schemaPath:"#/properties/annotations/items/properties/comment/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err16];
}
else {
vErrors.push(err16);
}
errors++;
}
}
}
else {
const err17 = {instancePath:instancePath+"/annotations/" + i0,schemaPath:"#/properties/annotations/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err17];
}
else {
vErrors.push(err17);
}
errors++;
}
}
}
else {
const err18 = {instancePath:instancePath+"/annotations",schemaPath:"#/properties/annotations/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err18];
}
else {
vErrors.push(err18);
}
errors++;
}
}
if(data.comment !== undefined){
if(typeof data.comment !== "string"){
const err19 = {instancePath:instancePath+"/comment",schemaPath:"#/properties/comment/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err19];
}
else {
vErrors.push(err19);
}
errors++;
}
}
if(data.creationInfo !== undefined){
let data8 = data.creationInfo;
if(data8 && typeof data8 == "object" && !Array.isArray(data8)){
if(data8.created === undefined){
const err20 = {instancePath:instancePath+"/creationInfo",schemaPath:"#/properties/creationInfo/required",keyword:"required",params:{missingProperty: "created"},message:"must have required property '"+"created"+"'"};
if(vErrors === null){
vErrors = [err20];
}
else {
vErrors.push(err20);
}
errors++;
}
if(data8.creators === undefined){
const err21 = {instancePath:instancePath+"/creationInfo",schemaPath:"#/properties/creationInfo/required",keyword:"required",params:{missingProperty: "creators"},message:"must have required property '"+"creators"+"'"};
if(vErrors === null){
vErrors = [err21];
}
else {
vErrors.push(err21);
}
errors++;
}
for(const key2 in data8){
if(!((((key2 === "comment") || (key2 === "created")) || (key2 === "creators")) || (key2 === "licenseListVersion"))){
const err22 = {instancePath:instancePath+"/creationInfo",schemaPath:"#/properties/creationInfo/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key2},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err22];
}
else {
vErrors.push(err22);
}
errors++;
}
}
if(data8.comment !== undefined){
if(typeof data8.comment !== "string"){
const err23 = {instancePath:instancePath+"/creationInfo/comment",schemaPath:"#/properties/creationInfo/properties/comment/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err23];
}
else {
vErrors.push(err23);
}
errors++;
}
}
if(data8.created !== undefined){
if(typeof data8.created !== "string"){
const err24 = {instancePath:instancePath+"/creationInfo/created",schemaPath:"#/properties/creationInfo/properties/created/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err24];
}
else {
vErrors.push(err24);
}
errors++;
}
}
if(data8.creators !== undefined){
let data11 = data8.creators;
if(Array.isArray(data11)){
if(data11.length < 1){
const err25 = {instancePath:instancePath+"/creationInfo/creators",schemaPath:"#/properties/creationInfo/properties/creators/minItems",keyword:"minItems",params:{limit: 1},message:"must NOT have fewer than 1 items"};
if(vErrors === null){
vErrors = [err25];
}
else {
vErrors.push(err25);
}
errors++;
}
const len1 = data11.length;
for(let i1=0; i1<len1; i1++){
if(typeof data11[i1] !== "string"){
const err26 = {instancePath:instancePath+"/creationInfo/creators/" + i1,schemaPath:"#/properties/creationInfo/properties/creators/items/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err26];
}
else {
vErrors.push(err26);
}
errors++;
}
}
}
else {
const err27 = {instancePath:instancePath+"/creationInfo/creators",schemaPath:"#/properties/creationInfo/properties/creators/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err27];
}
else {
vErrors.push(err27);
}
errors++;
}
}
if(data8.licenseListVersion !== undefined){
if(typeof data8.licenseListVersion !== "string"){
const err28 = {instancePath:instancePath+"/creationInfo/licenseListVersion",schemaPath:"#/properties/creationInfo/properties/licenseListVersion/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err28];
}
else {
vErrors.push(err28);
}
errors++;
}
}
}
else {
const err29 = {instancePath:instancePath+"/creationInfo",schemaPath:"#/properties/creationInfo/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err29];
}
else {
vErrors.push(err29);
}
errors++;
}
}
if(data.dataLicense !== undefined){
if(typeof data.dataLicense !== "string"){
const err30 = {instancePath:instancePath+"/dataLicense",schemaPath:"#/properties/dataLicense/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err30];
}
else {
vErrors.push(err30);
}
errors++;
}
}
if(data.externalDocumentRefs !== undefined){
let data15 = data.externalDocumentRefs;
if(Array.isArray(data15)){
const len2 = data15.length;
for(let i2=0; i2<len2; i2++){
let data16 = data15[i2];
if(data16 && typeof data16 == "object" && !Array.isArray(data16)){
if(data16.checksum === undefined){
const err31 = {instancePath:instancePath+"/externalDocumentRefs/" + i2,schemaPath:"#/properties/externalDocumentRefs/items/required",keyword:"required",params:{missingProperty: "checksum"},message:"must have required property '"+"checksum"+"'"};
if(vErrors === null){
vErrors = [err31];
}
else {
vErrors.push(err31);
}
errors++;
}
if(data16.externalDocumentId === undefined){
const err32 = {instancePath:instancePath+"/externalDocumentRefs/" + i2,schemaPath:"#/properties/externalDocumentRefs/items/required",keyword:"required",params:{missingProperty: "externalDocumentId"},message:"must have required property '"+"externalDocumentId"+"'"};
if(vErrors === null){
vErrors = [err32];
}
else {
vErrors.push(err32);
}
errors++;
}
if(data16.spdxDocument === undefined){
const err33 = {instancePath:instancePath+"/externalDocumentRefs/" + i2,schemaPath:"#/properties/externalDocumentRefs/items/required",keyword:"required",params:{missingProperty: "spdxDocument"},message:"must have required property '"+"spdxDocument"+"'"};
if(vErrors === null){
vErrors = [err33];
}
else {
vErrors.push(err33);
}
errors++;
}
for(const key3 in data16){
if(!(((key3 === "checksum") || (key3 === "externalDocumentId")) || (key3 === "spdxDocument"))){
const err34 = {instancePath:instancePath+"/externalDocumentRefs/" + i2,schemaPath:"#/properties/externalDocumentRefs/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key3},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err34];
}
else {
vErrors.push(err34);
}
errors++;
}
}
if(data16.checksum !== undefined){
let data17 = data16.checksum;
if(data17 && typeof data17 == "object" && !Array.isArray(data17)){
if(data17.algorithm === undefined){
const err35 = {instancePath:instancePath+"/externalDocumentRefs/" + i2+"/checksum",schemaPath:"#/properties/externalDocumentRefs/items/properties/checksum/required",keyword:"required",params:{missingProperty: "algorithm"},message:"must have required property '"+"algorithm"+"'"};
if(vErrors === null){
vErrors = [err35];
}
else {
vErrors.push(err35);
}
errors++;
}
if(data17.checksumValue === undefined){
const err36 = {instancePath:instancePath+"/externalDocumentRefs/" + i2+"/checksum",schemaPath:"#/properties/externalDocumentRefs/items/properties/checksum/required",keyword:"required",params:{missingProperty: "checksumValue"},message:"must have required property '"+"checksumValue"+"'"};
if(vErrors === null){
vErrors = [err36];
}
else {
vErrors.push(err36);
}
errors++;
}
for(const key4 in data17){
if(!((key4 === "algorithm") || (key4 === "checksumValue"))){
const err37 = {instancePath:instancePath+"/externalDocumentRefs/" + i2+"/checksum",schemaPath:"#/properties/externalDocumentRefs/items/properties/checksum/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key4},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err37];
}
else {
vErrors.push(err37);
}
errors++;
}
}
if(data17.algorithm !== undefined){
let data18 = data17.algorithm;
if(typeof data18 !== "string"){
const err38 = {instancePath:instancePath+"/externalDocumentRefs/" + i2+"/checksum/algorithm",schemaPath:"#/properties/externalDocumentRefs/items/properties/checksum/properties/algorithm/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err38];
}
else {
vErrors.push(err38);
}
errors++;
}
if(!(((((((((((((((((data18 === "SHA1") || (data18 === "BLAKE3")) || (data18 === "SHA3-384")) || (data18 === "SHA256")) || (data18 === "SHA384")) || (data18 === "BLAKE2b-512")) || (data18 === "BLAKE2b-256")) || (data18 === "SHA3-512")) || (data18 === "MD2")) || (data18 === "ADLER32")) || (data18 === "MD4")) || (data18 === "SHA3-256")) || (data18 === "BLAKE2b-384")) || (data18 === "SHA512")) || (data18 === "MD6")) || (data18 === "MD5")) || (data18 === "SHA224"))){
const err39 = {instancePath:instancePath+"/externalDocumentRefs/" + i2+"/checksum/algorithm",schemaPath:"#/properties/externalDocumentRefs/items/properties/checksum/properties/algorithm/enum",keyword:"enum",params:{allowedValues: schema11.properties.externalDocumentRefs.items.properties.checksum.properties.algorithm.enum},message:"must be equal to one of the allowed values"};
if(vErrors === null){
vErrors = [err39];
}
else {
vErrors.push(err39);
}
errors++;
}
}
if(data17.checksumValue !== undefined){
if(typeof data17.checksumValue !== "string"){
const err40 = {instancePath:instancePath+"/externalDocumentRefs/" + i2+"/checksum/checksumValue",schemaPath:"#/properties/externalDocumentRefs/items/properties/checksum/properties/checksumValue/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err40];
}
else {
vErrors.push(err40);
}
errors++;
}
}
}
else {
const err41 = {instancePath:instancePath+"/externalDocumentRefs/" + i2+"/checksum",schemaPath:"#/properties/externalDocumentRefs/items/properties/checksum/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err41];
}
else {
vErrors.push(err41);
}
errors++;
}
}
if(data16.externalDocumentId !== undefined){
if(typeof data16.externalDocumentId !== "string"){
const err42 = {instancePath:instancePath+"/externalDocumentRefs/" + i2+"/externalDocumentId",schemaPath:"#/properties/externalDocumentRefs/items/properties/externalDocumentId/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err42];
}
else {
vErrors.push(err42);
}
errors++;
}
}
if(data16.spdxDocument !== undefined){
if(typeof data16.spdxDocument !== "string"){
const err43 = {instancePath:instancePath+"/externalDocumentRefs/" + i2+"/spdxDocument",schemaPath:"#/properties/externalDocumentRefs/items/properties/spdxDocument/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err43];
}
else {
vErrors.push(err43);
}
errors++;
}
}
}
else {
const err44 = {instancePath:instancePath+"/externalDocumentRefs/" + i2,schemaPath:"#/properties/externalDocumentRefs/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err44];
}
else {
vErrors.push(err44);
}
errors++;
}
}
}
else {
const err45 = {instancePath:instancePath+"/externalDocumentRefs",schemaPath:"#/properties/externalDocumentRefs/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err45];
}
else {
vErrors.push(err45);
}
errors++;
}
}
if(data.hasExtractedLicensingInfos !== undefined){
let data22 = data.hasExtractedLicensingInfos;
if(Array.isArray(data22)){
const len3 = data22.length;
for(let i3=0; i3<len3; i3++){
let data23 = data22[i3];
if(data23 && typeof data23 == "object" && !Array.isArray(data23)){
if(data23.extractedText === undefined){
const err46 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3,schemaPath:"#/properties/hasExtractedLicensingInfos/items/required",keyword:"required",params:{missingProperty: "extractedText"},message:"must have required property '"+"extractedText"+"'"};
if(vErrors === null){
vErrors = [err46];
}
else {
vErrors.push(err46);
}
errors++;
}
if(data23.licenseId === undefined){
const err47 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3,schemaPath:"#/properties/hasExtractedLicensingInfos/items/required",keyword:"required",params:{missingProperty: "licenseId"},message:"must have required property '"+"licenseId"+"'"};
if(vErrors === null){
vErrors = [err47];
}
else {
vErrors.push(err47);
}
errors++;
}
for(const key5 in data23){
if(!((((((key5 === "comment") || (key5 === "crossRefs")) || (key5 === "extractedText")) || (key5 === "licenseId")) || (key5 === "name")) || (key5 === "seeAlsos"))){
const err48 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3,schemaPath:"#/properties/hasExtractedLicensingInfos/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key5},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err48];
}
else {
vErrors.push(err48);
}
errors++;
}
}
if(data23.comment !== undefined){
if(typeof data23.comment !== "string"){
const err49 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/comment",schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/comment/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err49];
}
else {
vErrors.push(err49);
}
errors++;
}
}
if(data23.crossRefs !== undefined){
let data25 = data23.crossRefs;
if(Array.isArray(data25)){
const len4 = data25.length;
for(let i4=0; i4<len4; i4++){
let data26 = data25[i4];
if(data26 && typeof data26 == "object" && !Array.isArray(data26)){
if(data26.url === undefined){
const err50 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/crossRefs/" + i4,schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/crossRefs/items/required",keyword:"required",params:{missingProperty: "url"},message:"must have required property '"+"url"+"'"};
if(vErrors === null){
vErrors = [err50];
}
else {
vErrors.push(err50);
}
errors++;
}
for(const key6 in data26){
if(!(((((((key6 === "isLive") || (key6 === "isValid")) || (key6 === "isWayBackLink")) || (key6 === "match")) || (key6 === "order")) || (key6 === "timestamp")) || (key6 === "url"))){
const err51 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/crossRefs/" + i4,schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/crossRefs/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key6},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err51];
}
else {
vErrors.push(err51);
}
errors++;
}
}
if(data26.isLive !== undefined){
if(typeof data26.isLive !== "boolean"){
const err52 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/crossRefs/" + i4+"/isLive",schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/crossRefs/items/properties/isLive/type",keyword:"type",params:{type: "boolean"},message:"must be boolean"};
if(vErrors === null){
vErrors = [err52];
}
else {
vErrors.push(err52);
}
errors++;
}
}
if(data26.isValid !== undefined){
if(typeof data26.isValid !== "boolean"){
const err53 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/crossRefs/" + i4+"/isValid",schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/crossRefs/items/properties/isValid/type",keyword:"type",params:{type: "boolean"},message:"must be boolean"};
if(vErrors === null){
vErrors = [err53];
}
else {
vErrors.push(err53);
}
errors++;
}
}
if(data26.isWayBackLink !== undefined){
if(typeof data26.isWayBackLink !== "boolean"){
const err54 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/crossRefs/" + i4+"/isWayBackLink",schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/crossRefs/items/properties/isWayBackLink/type",keyword:"type",params:{type: "boolean"},message:"must be boolean"};
if(vErrors === null){
vErrors = [err54];
}
else {
vErrors.push(err54);
}
errors++;
}
}
if(data26.match !== undefined){
if(typeof data26.match !== "string"){
const err55 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/crossRefs/" + i4+"/match",schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/crossRefs/items/properties/match/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err55];
}
else {
vErrors.push(err55);
}
errors++;
}
}
if(data26.order !== undefined){
let data31 = data26.order;
if(!((typeof data31 == "number") && (!(data31 % 1) && !isNaN(data31)))){
const err56 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/crossRefs/" + i4+"/order",schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/crossRefs/items/properties/order/type",keyword:"type",params:{type: "integer"},message:"must be integer"};
if(vErrors === null){
vErrors = [err56];
}
else {
vErrors.push(err56);
}
errors++;
}
}
if(data26.timestamp !== undefined){
if(typeof data26.timestamp !== "string"){
const err57 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/crossRefs/" + i4+"/timestamp",schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/crossRefs/items/properties/timestamp/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err57];
}
else {
vErrors.push(err57);
}
errors++;
}
}
if(data26.url !== undefined){
if(typeof data26.url !== "string"){
const err58 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/crossRefs/" + i4+"/url",schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/crossRefs/items/properties/url/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err58];
}
else {
vErrors.push(err58);
}
errors++;
}
}
}
else {
const err59 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/crossRefs/" + i4,schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/crossRefs/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err59];
}
else {
vErrors.push(err59);
}
errors++;
}
}
}
else {
const err60 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/crossRefs",schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/crossRefs/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err60];
}
else {
vErrors.push(err60);
}
errors++;
}
}
if(data23.extractedText !== undefined){
if(typeof data23.extractedText !== "string"){
const err61 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/extractedText",schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/extractedText/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err61];
}
else {
vErrors.push(err61);
}
errors++;
}
}
if(data23.licenseId !== undefined){
if(typeof data23.licenseId !== "string"){
const err62 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/licenseId",schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/licenseId/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err62];
}
else {
vErrors.push(err62);
}
errors++;
}
}
if(data23.name !== undefined){
if(typeof data23.name !== "string"){
const err63 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/name",schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/name/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err63];
}
else {
vErrors.push(err63);
}
errors++;
}
}
if(data23.seeAlsos !== undefined){
let data37 = data23.seeAlsos;
if(Array.isArray(data37)){
const len5 = data37.length;
for(let i5=0; i5<len5; i5++){
if(typeof data37[i5] !== "string"){
const err64 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/seeAlsos/" + i5,schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/seeAlsos/items/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err64];
}
else {
vErrors.push(err64);
}
errors++;
}
}
}
else {
const err65 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3+"/seeAlsos",schemaPath:"#/properties/hasExtractedLicensingInfos/items/properties/seeAlsos/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err65];
}
else {
vErrors.push(err65);
}
errors++;
}
}
}
else {
const err66 = {instancePath:instancePath+"/hasExtractedLicensingInfos/" + i3,schemaPath:"#/properties/hasExtractedLicensingInfos/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err66];
}
else {
vErrors.push(err66);
}
errors++;
}
}
}
else {
const err67 = {instancePath:instancePath+"/hasExtractedLicensingInfos",schemaPath:"#/properties/hasExtractedLicensingInfos/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err67];
}
else {
vErrors.push(err67);
}
errors++;
}
}
if(data.name !== undefined){
if(typeof data.name !== "string"){
const err68 = {instancePath:instancePath+"/name",schemaPath:"#/properties/name/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err68];
}
else {
vErrors.push(err68);
}
errors++;
}
}
if(data.revieweds !== undefined){
let data40 = data.revieweds;
if(Array.isArray(data40)){
const len6 = data40.length;
for(let i6=0; i6<len6; i6++){
let data41 = data40[i6];
if(data41 && typeof data41 == "object" && !Array.isArray(data41)){
if(data41.reviewDate === undefined){
const err69 = {instancePath:instancePath+"/revieweds/" + i6,schemaPath:"#/properties/revieweds/items/required",keyword:"required",params:{missingProperty: "reviewDate"},message:"must have required property '"+"reviewDate"+"'"};
if(vErrors === null){
vErrors = [err69];
}
else {
vErrors.push(err69);
}
errors++;
}
for(const key7 in data41){
if(!(((key7 === "comment") || (key7 === "reviewDate")) || (key7 === "reviewer"))){
const err70 = {instancePath:instancePath+"/revieweds/" + i6,schemaPath:"#/properties/revieweds/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key7},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err70];
}
else {
vErrors.push(err70);
}
errors++;
}
}
if(data41.comment !== undefined){
if(typeof data41.comment !== "string"){
const err71 = {instancePath:instancePath+"/revieweds/" + i6+"/comment",schemaPath:"#/properties/revieweds/items/properties/comment/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err71];
}
else {
vErrors.push(err71);
}
errors++;
}
}
if(data41.reviewDate !== undefined){
if(typeof data41.reviewDate !== "string"){
const err72 = {instancePath:instancePath+"/revieweds/" + i6+"/reviewDate",schemaPath:"#/properties/revieweds/items/properties/reviewDate/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err72];
}
else {
vErrors.push(err72);
}
errors++;
}
}
if(data41.reviewer !== undefined){
if(typeof data41.reviewer !== "string"){
const err73 = {instancePath:instancePath+"/revieweds/" + i6+"/reviewer",schemaPath:"#/properties/revieweds/items/properties/reviewer/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err73];
}
else {
vErrors.push(err73);
}
errors++;
}
}
}
else {
const err74 = {instancePath:instancePath+"/revieweds/" + i6,schemaPath:"#/properties/revieweds/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err74];
}
else {
vErrors.push(err74);
}
errors++;
}
}
}
else {
const err75 = {instancePath:instancePath+"/revieweds",schemaPath:"#/properties/revieweds/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err75];
}
else {
vErrors.push(err75);
}
errors++;
}
}
if(data.spdxVersion !== undefined){
if(typeof data.spdxVersion !== "string"){
const err76 = {instancePath:instancePath+"/spdxVersion",schemaPath:"#/properties/spdxVersion/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err76];
}
else {
vErrors.push(err76);
}
errors++;
}
}
if(data.documentNamespace !== undefined){
if(typeof data.documentNamespace !== "string"){
const err77 = {instancePath:instancePath+"/documentNamespace",schemaPath:"#/properties/documentNamespace/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err77];
}
else {
vErrors.push(err77);
}
errors++;
}
}
if(data.documentDescribes !== undefined){
let data47 = data.documentDescribes;
if(Array.isArray(data47)){
const len7 = data47.length;
for(let i7=0; i7<len7; i7++){
if(typeof data47[i7] !== "string"){
const err78 = {instancePath:instancePath+"/documentDescribes/" + i7,schemaPath:"#/properties/documentDescribes/items/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err78];
}
else {
vErrors.push(err78);
}
errors++;
}
}
}
else {
const err79 = {instancePath:instancePath+"/documentDescribes",schemaPath:"#/properties/documentDescribes/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err79];
}
else {
vErrors.push(err79);
}
errors++;
}
}
if(data.packages !== undefined){
let data49 = data.packages;
if(Array.isArray(data49)){
const len8 = data49.length;
for(let i8=0; i8<len8; i8++){
let data50 = data49[i8];
if(data50 && typeof data50 == "object" && !Array.isArray(data50)){
if(data50.SPDXID === undefined){
const err80 = {instancePath:instancePath+"/packages/" + i8,schemaPath:"#/properties/packages/items/required",keyword:"required",params:{missingProperty: "SPDXID"},message:"must have required property '"+"SPDXID"+"'"};
if(vErrors === null){
vErrors = [err80];
}
else {
vErrors.push(err80);
}
errors++;
}
if(data50.downloadLocation === undefined){
const err81 = {instancePath:instancePath+"/packages/" + i8,schemaPath:"#/properties/packages/items/required",keyword:"required",params:{missingProperty: "downloadLocation"},message:"must have required property '"+"downloadLocation"+"'"};
if(vErrors === null){
vErrors = [err81];
}
else {
vErrors.push(err81);
}
errors++;
}
if(data50.name === undefined){
const err82 = {instancePath:instancePath+"/packages/" + i8,schemaPath:"#/properties/packages/items/required",keyword:"required",params:{missingProperty: "name"},message:"must have required property '"+"name"+"'"};
if(vErrors === null){
vErrors = [err82];
}
else {
vErrors.push(err82);
}
errors++;
}
for(const key8 in data50){
if(!(func2.call(schema11.properties.packages.items.properties, key8))){
const err83 = {instancePath:instancePath+"/packages/" + i8,schemaPath:"#/properties/packages/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key8},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err83];
}
else {
vErrors.push(err83);
}
errors++;
}
}
if(data50.SPDXID !== undefined){
if(typeof data50.SPDXID !== "string"){
const err84 = {instancePath:instancePath+"/packages/" + i8+"/SPDXID",schemaPath:"#/properties/packages/items/properties/SPDXID/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err84];
}
else {
vErrors.push(err84);
}
errors++;
}
}
if(data50.annotations !== undefined){
let data52 = data50.annotations;
if(Array.isArray(data52)){
const len9 = data52.length;
for(let i9=0; i9<len9; i9++){
let data53 = data52[i9];
if(data53 && typeof data53 == "object" && !Array.isArray(data53)){
if(data53.annotationDate === undefined){
const err85 = {instancePath:instancePath+"/packages/" + i8+"/annotations/" + i9,schemaPath:"#/properties/packages/items/properties/annotations/items/required",keyword:"required",params:{missingProperty: "annotationDate"},message:"must have required property '"+"annotationDate"+"'"};
if(vErrors === null){
vErrors = [err85];
}
else {
vErrors.push(err85);
}
errors++;
}
if(data53.annotationType === undefined){
const err86 = {instancePath:instancePath+"/packages/" + i8+"/annotations/" + i9,schemaPath:"#/properties/packages/items/properties/annotations/items/required",keyword:"required",params:{missingProperty: "annotationType"},message:"must have required property '"+"annotationType"+"'"};
if(vErrors === null){
vErrors = [err86];
}
else {
vErrors.push(err86);
}
errors++;
}
if(data53.annotator === undefined){
const err87 = {instancePath:instancePath+"/packages/" + i8+"/annotations/" + i9,schemaPath:"#/properties/packages/items/properties/annotations/items/required",keyword:"required",params:{missingProperty: "annotator"},message:"must have required property '"+"annotator"+"'"};
if(vErrors === null){
vErrors = [err87];
}
else {
vErrors.push(err87);
}
errors++;
}
if(data53.comment === undefined){
const err88 = {instancePath:instancePath+"/packages/" + i8+"/annotations/" + i9,schemaPath:"#/properties/packages/items/properties/annotations/items/required",keyword:"required",params:{missingProperty: "comment"},message:"must have required property '"+"comment"+"'"};
if(vErrors === null){
vErrors = [err88];
}
else {
vErrors.push(err88);
}
errors++;
}
for(const key9 in data53){
if(!((((key9 === "annotationDate") || (key9 === "annotationType")) || (key9 === "annotator")) || (key9 === "comment"))){
const err89 = {instancePath:instancePath+"/packages/" + i8+"/annotations/" + i9,schemaPath:"#/properties/packages/items/properties/annotations/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key9},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err89];
}
else {
vErrors.push(err89);
}
errors++;
}
}
if(data53.annotationDate !== undefined){
if(typeof data53.annotationDate !== "string"){
const err90 = {instancePath:instancePath+"/packages/" + i8+"/annotations/" + i9+"/annotationDate",schemaPath:"#/properties/packages/items/properties/annotations/items/properties/annotationDate/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err90];
}
else {
vErrors.push(err90);
}
errors++;
}
}
if(data53.annotationType !== undefined){
let data55 = data53.annotationType;
if(typeof data55 !== "string"){
const err91 = {instancePath:instancePath+"/packages/" + i8+"/annotations/" + i9+"/annotationType",schemaPath:"#/properties/packages/items/properties/annotations/items/properties/annotationType/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err91];
}
else {
vErrors.push(err91);
}
errors++;
}
if(!((data55 === "OTHER") || (data55 === "REVIEW"))){
const err92 = {instancePath:instancePath+"/packages/" + i8+"/annotations/" + i9+"/annotationType",schemaPath:"#/properties/packages/items/properties/annotations/items/properties/annotationType/enum",keyword:"enum",params:{allowedValues: schema11.properties.packages.items.properties.annotations.items.properties.annotationType.enum},message:"must be equal to one of the allowed values"};
if(vErrors === null){
vErrors = [err92];
}
else {
vErrors.push(err92);
}
errors++;
}
}
if(data53.annotator !== undefined){
if(typeof data53.annotator !== "string"){
const err93 = {instancePath:instancePath+"/packages/" + i8+"/annotations/" + i9+"/annotator",schemaPath:"#/properties/packages/items/properties/annotations/items/properties/annotator/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err93];
}
else {
vErrors.push(err93);
}
errors++;
}
}
if(data53.comment !== undefined){
if(typeof data53.comment !== "string"){
const err94 = {instancePath:instancePath+"/packages/" + i8+"/annotations/" + i9+"/comment",schemaPath:"#/properties/packages/items/properties/annotations/items/properties/comment/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err94];
}
else {
vErrors.push(err94);
}
errors++;
}
}
}
else {
const err95 = {instancePath:instancePath+"/packages/" + i8+"/annotations/" + i9,schemaPath:"#/properties/packages/items/properties/annotations/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err95];
}
else {
vErrors.push(err95);
}
errors++;
}
}
}
else {
const err96 = {instancePath:instancePath+"/packages/" + i8+"/annotations",schemaPath:"#/properties/packages/items/properties/annotations/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err96];
}
else {
vErrors.push(err96);
}
errors++;
}
}
if(data50.attributionTexts !== undefined){
let data58 = data50.attributionTexts;
if(Array.isArray(data58)){
const len10 = data58.length;
for(let i10=0; i10<len10; i10++){
if(typeof data58[i10] !== "string"){
const err97 = {instancePath:instancePath+"/packages/" + i8+"/attributionTexts/" + i10,schemaPath:"#/properties/packages/items/properties/attributionTexts/items/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err97];
}
else {
vErrors.push(err97);
}
errors++;
}
}
}
else {
const err98 = {instancePath:instancePath+"/packages/" + i8+"/attributionTexts",schemaPath:"#/properties/packages/items/properties/attributionTexts/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err98];
}
else {
vErrors.push(err98);
}
errors++;
}
}
if(data50.builtDate !== undefined){
if(typeof data50.builtDate !== "string"){
const err99 = {instancePath:instancePath+"/packages/" + i8+"/builtDate",schemaPath:"#/properties/packages/items/properties/builtDate/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err99];
}
else {
vErrors.push(err99);
}
errors++;
}
}
if(data50.checksums !== undefined){
let data61 = data50.checksums;
if(Array.isArray(data61)){
const len11 = data61.length;
for(let i11=0; i11<len11; i11++){
let data62 = data61[i11];
if(data62 && typeof data62 == "object" && !Array.isArray(data62)){
if(data62.algorithm === undefined){
const err100 = {instancePath:instancePath+"/packages/" + i8+"/checksums/" + i11,schemaPath:"#/properties/packages/items/properties/checksums/items/required",keyword:"required",params:{missingProperty: "algorithm"},message:"must have required property '"+"algorithm"+"'"};
if(vErrors === null){
vErrors = [err100];
}
else {
vErrors.push(err100);
}
errors++;
}
if(data62.checksumValue === undefined){
const err101 = {instancePath:instancePath+"/packages/" + i8+"/checksums/" + i11,schemaPath:"#/properties/packages/items/properties/checksums/items/required",keyword:"required",params:{missingProperty: "checksumValue"},message:"must have required property '"+"checksumValue"+"'"};
if(vErrors === null){
vErrors = [err101];
}
else {
vErrors.push(err101);
}
errors++;
}
for(const key10 in data62){
if(!((key10 === "algorithm") || (key10 === "checksumValue"))){
const err102 = {instancePath:instancePath+"/packages/" + i8+"/checksums/" + i11,schemaPath:"#/properties/packages/items/properties/checksums/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key10},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err102];
}
else {
vErrors.push(err102);
}
errors++;
}
}
if(data62.algorithm !== undefined){
let data63 = data62.algorithm;
if(typeof data63 !== "string"){
const err103 = {instancePath:instancePath+"/packages/" + i8+"/checksums/" + i11+"/algorithm",schemaPath:"#/properties/packages/items/properties/checksums/items/properties/algorithm/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err103];
}
else {
vErrors.push(err103);
}
errors++;
}
if(!(((((((((((((((((data63 === "SHA1") || (data63 === "BLAKE3")) || (data63 === "SHA3-384")) || (data63 === "SHA256")) || (data63 === "SHA384")) || (data63 === "BLAKE2b-512")) || (data63 === "BLAKE2b-256")) || (data63 === "SHA3-512")) || (data63 === "MD2")) || (data63 === "ADLER32")) || (data63 === "MD4")) || (data63 === "SHA3-256")) || (data63 === "BLAKE2b-384")) || (data63 === "SHA512")) || (data63 === "MD6")) || (data63 === "MD5")) || (data63 === "SHA224"))){
const err104 = {instancePath:instancePath+"/packages/" + i8+"/checksums/" + i11+"/algorithm",schemaPath:"#/properties/packages/items/properties/checksums/items/properties/algorithm/enum",keyword:"enum",params:{allowedValues: schema11.properties.packages.items.properties.checksums.items.properties.algorithm.enum},message:"must be equal to one of the allowed values"};
if(vErrors === null){
vErrors = [err104];
}
else {
vErrors.push(err104);
}
errors++;
}
}
if(data62.checksumValue !== undefined){
if(typeof data62.checksumValue !== "string"){
const err105 = {instancePath:instancePath+"/packages/" + i8+"/checksums/" + i11+"/checksumValue",schemaPath:"#/properties/packages/items/properties/checksums/items/properties/checksumValue/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err105];
}
else {
vErrors.push(err105);
}
errors++;
}
}
}
else {
const err106 = {instancePath:instancePath+"/packages/" + i8+"/checksums/" + i11,schemaPath:"#/properties/packages/items/properties/checksums/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err106];
}
else {
vErrors.push(err106);
}
errors++;
}
}
}
else {
const err107 = {instancePath:instancePath+"/packages/" + i8+"/checksums",schemaPath:"#/properties/packages/items/properties/checksums/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err107];
}
else {
vErrors.push(err107);
}
errors++;
}
}
if(data50.comment !== undefined){
if(typeof data50.comment !== "string"){
const err108 = {instancePath:instancePath+"/packages/" + i8+"/comment",schemaPath:"#/properties/packages/items/properties/comment/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err108];
}
else {
vErrors.push(err108);
}
errors++;
}
}
if(data50.copyrightText !== undefined){
if(typeof data50.copyrightText !== "string"){
const err109 = {instancePath:instancePath+"/packages/" + i8+"/copyrightText",schemaPath:"#/properties/packages/items/properties/copyrightText/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err109];
}
else {
vErrors.push(err109);
}
errors++;
}
}
if(data50.description !== undefined){
if(typeof data50.description !== "string"){
const err110 = {instancePath:instancePath+"/packages/" + i8+"/description",schemaPath:"#/properties/packages/items/properties/description/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err110];
}
else {
vErrors.push(err110);
}
errors++;
}
}
if(data50.downloadLocation !== undefined){
if(typeof data50.downloadLocation !== "string"){
const err111 = {instancePath:instancePath+"/packages/" + i8+"/downloadLocation",schemaPath:"#/properties/packages/items/properties/downloadLocation/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err111];
}
else {
vErrors.push(err111);
}
errors++;
}
}
if(data50.externalRefs !== undefined){
let data69 = data50.externalRefs;
if(Array.isArray(data69)){
const len12 = data69.length;
for(let i12=0; i12<len12; i12++){
let data70 = data69[i12];
if(data70 && typeof data70 == "object" && !Array.isArray(data70)){
if(data70.referenceCategory === undefined){
const err112 = {instancePath:instancePath+"/packages/" + i8+"/externalRefs/" + i12,schemaPath:"#/properties/packages/items/properties/externalRefs/items/required",keyword:"required",params:{missingProperty: "referenceCategory"},message:"must have required property '"+"referenceCategory"+"'"};
if(vErrors === null){
vErrors = [err112];
}
else {
vErrors.push(err112);
}
errors++;
}
if(data70.referenceLocator === undefined){
const err113 = {instancePath:instancePath+"/packages/" + i8+"/externalRefs/" + i12,schemaPath:"#/properties/packages/items/properties/externalRefs/items/required",keyword:"required",params:{missingProperty: "referenceLocator"},message:"must have required property '"+"referenceLocator"+"'"};
if(vErrors === null){
vErrors = [err113];
}
else {
vErrors.push(err113);
}
errors++;
}
if(data70.referenceType === undefined){
const err114 = {instancePath:instancePath+"/packages/" + i8+"/externalRefs/" + i12,schemaPath:"#/properties/packages/items/properties/externalRefs/items/required",keyword:"required",params:{missingProperty: "referenceType"},message:"must have required property '"+"referenceType"+"'"};
if(vErrors === null){
vErrors = [err114];
}
else {
vErrors.push(err114);
}
errors++;
}
for(const key11 in data70){
if(!((((key11 === "comment") || (key11 === "referenceCategory")) || (key11 === "referenceLocator")) || (key11 === "referenceType"))){
const err115 = {instancePath:instancePath+"/packages/" + i8+"/externalRefs/" + i12,schemaPath:"#/properties/packages/items/properties/externalRefs/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key11},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err115];
}
else {
vErrors.push(err115);
}
errors++;
}
}
if(data70.comment !== undefined){
if(typeof data70.comment !== "string"){
const err116 = {instancePath:instancePath+"/packages/" + i8+"/externalRefs/" + i12+"/comment",schemaPath:"#/properties/packages/items/properties/externalRefs/items/properties/comment/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err116];
}
else {
vErrors.push(err116);
}
errors++;
}
}
if(data70.referenceCategory !== undefined){
let data72 = data70.referenceCategory;
if(typeof data72 !== "string"){
const err117 = {instancePath:instancePath+"/packages/" + i8+"/externalRefs/" + i12+"/referenceCategory",schemaPath:"#/properties/packages/items/properties/externalRefs/items/properties/referenceCategory/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err117];
}
else {
vErrors.push(err117);
}
errors++;
}
if(!((((data72 === "OTHER") || (data72 === "PERSISTENT-ID")) || (data72 === "SECURITY")) || (data72 === "PACKAGE-MANAGER"))){
const err118 = {instancePath:instancePath+"/packages/" + i8+"/externalRefs/" + i12+"/referenceCategory",schemaPath:"#/properties/packages/items/properties/externalRefs/items/properties/referenceCategory/enum",keyword:"enum",params:{allowedValues: schema11.properties.packages.items.properties.externalRefs.items.properties.referenceCategory.enum},message:"must be equal to one of the allowed values"};
if(vErrors === null){
vErrors = [err118];
}
else {
vErrors.push(err118);
}
errors++;
}
}
if(data70.referenceLocator !== undefined){
if(typeof data70.referenceLocator !== "string"){
const err119 = {instancePath:instancePath+"/packages/" + i8+"/externalRefs/" + i12+"/referenceLocator",schemaPath:"#/properties/packages/items/properties/externalRefs/items/properties/referenceLocator/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err119];
}
else {
vErrors.push(err119);
}
errors++;
}
}
if(data70.referenceType !== undefined){
if(typeof data70.referenceType !== "string"){
const err120 = {instancePath:instancePath+"/packages/" + i8+"/externalRefs/" + i12+"/referenceType",schemaPath:"#/properties/packages/items/properties/externalRefs/items/properties/referenceType/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err120];
}
else {
vErrors.push(err120);
}
errors++;
}
}
}
else {
const err121 = {instancePath:instancePath+"/packages/" + i8+"/externalRefs/" + i12,schemaPath:"#/properties/packages/items/properties/externalRefs/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err121];
}
else {
vErrors.push(err121);
}
errors++;
}
}
}
else {
const err122 = {instancePath:instancePath+"/packages/" + i8+"/externalRefs",schemaPath:"#/properties/packages/items/properties/externalRefs/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err122];
}
else {
vErrors.push(err122);
}
errors++;
}
}
if(data50.filesAnalyzed !== undefined){
if(typeof data50.filesAnalyzed !== "boolean"){
const err123 = {instancePath:instancePath+"/packages/" + i8+"/filesAnalyzed",schemaPath:"#/properties/packages/items/properties/filesAnalyzed/type",keyword:"type",params:{type: "boolean"},message:"must be boolean"};
if(vErrors === null){
vErrors = [err123];
}
else {
vErrors.push(err123);
}
errors++;
}
}
if(data50.hasFiles !== undefined){
let data76 = data50.hasFiles;
if(Array.isArray(data76)){
const len13 = data76.length;
for(let i13=0; i13<len13; i13++){
if(typeof data76[i13] !== "string"){
const err124 = {instancePath:instancePath+"/packages/" + i8+"/hasFiles/" + i13,schemaPath:"#/properties/packages/items/properties/hasFiles/items/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err124];
}
else {
vErrors.push(err124);
}
errors++;
}
}
}
else {
const err125 = {instancePath:instancePath+"/packages/" + i8+"/hasFiles",schemaPath:"#/properties/packages/items/properties/hasFiles/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err125];
}
else {
vErrors.push(err125);
}
errors++;
}
}
if(data50.homepage !== undefined){
if(typeof data50.homepage !== "string"){
const err126 = {instancePath:instancePath+"/packages/" + i8+"/homepage",schemaPath:"#/properties/packages/items/properties/homepage/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err126];
}
else {
vErrors.push(err126);
}
errors++;
}
}
if(data50.licenseComments !== undefined){
if(typeof data50.licenseComments !== "string"){
const err127 = {instancePath:instancePath+"/packages/" + i8+"/licenseComments",schemaPath:"#/properties/packages/items/properties/licenseComments/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err127];
}
else {
vErrors.push(err127);
}
errors++;
}
}
if(data50.licenseConcluded !== undefined){
if(typeof data50.licenseConcluded !== "string"){
const err128 = {instancePath:instancePath+"/packages/" + i8+"/licenseConcluded",schemaPath:"#/properties/packages/items/properties/licenseConcluded/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err128];
}
else {
vErrors.push(err128);
}
errors++;
}
}
if(data50.licenseDeclared !== undefined){
if(typeof data50.licenseDeclared !== "string"){
const err129 = {instancePath:instancePath+"/packages/" + i8+"/licenseDeclared",schemaPath:"#/properties/packages/items/properties/licenseDeclared/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err129];
}
else {
vErrors.push(err129);
}
errors++;
}
}
if(data50.licenseInfoFromFiles !== undefined){
let data82 = data50.licenseInfoFromFiles;
if(Array.isArray(data82)){
const len14 = data82.length;
for(let i14=0; i14<len14; i14++){
if(typeof data82[i14] !== "string"){
const err130 = {instancePath:instancePath+"/packages/" + i8+"/licenseInfoFromFiles/" + i14,schemaPath:"#/properties/packages/items/properties/licenseInfoFromFiles/items/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err130];
}
else {
vErrors.push(err130);
}
errors++;
}
}
}
else {
const err131 = {instancePath:instancePath+"/packages/" + i8+"/licenseInfoFromFiles",schemaPath:"#/properties/packages/items/properties/licenseInfoFromFiles/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err131];
}
else {
vErrors.push(err131);
}
errors++;
}
}
if(data50.name !== undefined){
if(typeof data50.name !== "string"){
const err132 = {instancePath:instancePath+"/packages/" + i8+"/name",schemaPath:"#/properties/packages/items/properties/name/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err132];
}
else {
vErrors.push(err132);
}
errors++;
}
}
if(data50.originator !== undefined){
if(typeof data50.originator !== "string"){
const err133 = {instancePath:instancePath+"/packages/" + i8+"/originator",schemaPath:"#/properties/packages/items/properties/originator/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err133];
}
else {
vErrors.push(err133);
}
errors++;
}
}
if(data50.packageFileName !== undefined){
if(typeof data50.packageFileName !== "string"){
const err134 = {instancePath:instancePath+"/packages/" + i8+"/packageFileName",schemaPath:"#/properties/packages/items/properties/packageFileName/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err134];
}
else {
vErrors.push(err134);
}
errors++;
}
}
if(data50.packageVerificationCode !== undefined){
let data87 = data50.packageVerificationCode;
if(data87 && typeof data87 == "object" && !Array.isArray(data87)){
if(data87.packageVerificationCodeValue === undefined){
const err135 = {instancePath:instancePath+"/packages/" + i8+"/packageVerificationCode",schemaPath:"#/properties/packages/items/properties/packageVerificationCode/required",keyword:"required",params:{missingProperty: "packageVerificationCodeValue"},message:"must have required property '"+"packageVerificationCodeValue"+"'"};
if(vErrors === null){
vErrors = [err135];
}
else {
vErrors.push(err135);
}
errors++;
}
for(const key12 in data87){
if(!((key12 === "packageVerificationCodeExcludedFiles") || (key12 === "packageVerificationCodeValue"))){
const err136 = {instancePath:instancePath+"/packages/" + i8+"/packageVerificationCode",schemaPath:"#/properties/packages/items/properties/packageVerificationCode/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key12},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err136];
}
else {
vErrors.push(err136);
}
errors++;
}
}
if(data87.packageVerificationCodeExcludedFiles !== undefined){
let data88 = data87.packageVerificationCodeExcludedFiles;
if(Array.isArray(data88)){
const len15 = data88.length;
for(let i15=0; i15<len15; i15++){
if(typeof data88[i15] !== "string"){
const err137 = {instancePath:instancePath+"/packages/" + i8+"/packageVerificationCode/packageVerificationCodeExcludedFiles/" + i15,schemaPath:"#/properties/packages/items/properties/packageVerificationCode/properties/packageVerificationCodeExcludedFiles/items/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err137];
}
else {
vErrors.push(err137);
}
errors++;
}
}
}
else {
const err138 = {instancePath:instancePath+"/packages/" + i8+"/packageVerificationCode/packageVerificationCodeExcludedFiles",schemaPath:"#/properties/packages/items/properties/packageVerificationCode/properties/packageVerificationCodeExcludedFiles/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err138];
}
else {
vErrors.push(err138);
}
errors++;
}
}
if(data87.packageVerificationCodeValue !== undefined){
if(typeof data87.packageVerificationCodeValue !== "string"){
const err139 = {instancePath:instancePath+"/packages/" + i8+"/packageVerificationCode/packageVerificationCodeValue",schemaPath:"#/properties/packages/items/properties/packageVerificationCode/properties/packageVerificationCodeValue/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err139];
}
else {
vErrors.push(err139);
}
errors++;
}
}
}
else {
const err140 = {instancePath:instancePath+"/packages/" + i8+"/packageVerificationCode",schemaPath:"#/properties/packages/items/properties/packageVerificationCode/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err140];
}
else {
vErrors.push(err140);
}
errors++;
}
}
if(data50.primaryPackagePurpose !== undefined){
let data91 = data50.primaryPackagePurpose;
if(typeof data91 !== "string"){
const err141 = {instancePath:instancePath+"/packages/" + i8+"/primaryPackagePurpose",schemaPath:"#/properties/packages/items/properties/primaryPackagePurpose/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err141];
}
else {
vErrors.push(err141);
}
errors++;
}
if(!((((((((((((data91 === "OTHER") || (data91 === "INSTALL")) || (data91 === "ARCHIVE")) || (data91 === "FIRMWARE")) || (data91 === "APPLICATION")) || (data91 === "FRAMEWORK")) || (data91 === "LIBRARY")) || (data91 === "CONTAINER")) || (data91 === "SOURCE")) || (data91 === "DEVICE")) || (data91 === "OPERATING_SYSTEM")) || (data91 === "FILE"))){
const err142 = {instancePath:instancePath+"/packages/" + i8+"/primaryPackagePurpose",schemaPath:"#/properties/packages/items/properties/primaryPackagePurpose/enum",keyword:"enum",params:{allowedValues: schema11.properties.packages.items.properties.primaryPackagePurpose.enum},message:"must be equal to one of the allowed values"};
if(vErrors === null){
vErrors = [err142];
}
else {
vErrors.push(err142);
}
errors++;
}
}
if(data50.releaseDate !== undefined){
if(typeof data50.releaseDate !== "string"){
const err143 = {instancePath:instancePath+"/packages/" + i8+"/releaseDate",schemaPath:"#/properties/packages/items/properties/releaseDate/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err143];
}
else {
vErrors.push(err143);
}
errors++;
}
}
if(data50.sourceInfo !== undefined){
if(typeof data50.sourceInfo !== "string"){
const err144 = {instancePath:instancePath+"/packages/" + i8+"/sourceInfo",schemaPath:"#/properties/packages/items/properties/sourceInfo/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err144];
}
else {
vErrors.push(err144);
}
errors++;
}
}
if(data50.summary !== undefined){
if(typeof data50.summary !== "string"){
const err145 = {instancePath:instancePath+"/packages/" + i8+"/summary",schemaPath:"#/properties/packages/items/properties/summary/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err145];
}
else {
vErrors.push(err145);
}
errors++;
}
}
if(data50.supplier !== undefined){
if(typeof data50.supplier !== "string"){
const err146 = {instancePath:instancePath+"/packages/" + i8+"/supplier",schemaPath:"#/properties/packages/items/properties/supplier/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err146];
}
else {
vErrors.push(err146);
}
errors++;
}
}
if(data50.validUntilDate !== undefined){
if(typeof data50.validUntilDate !== "string"){
const err147 = {instancePath:instancePath+"/packages/" + i8+"/validUntilDate",schemaPath:"#/properties/packages/items/properties/validUntilDate/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err147];
}
else {
vErrors.push(err147);
}
errors++;
}
}
if(data50.versionInfo !== undefined){
if(typeof data50.versionInfo !== "string"){
const err148 = {instancePath:instancePath+"/packages/" + i8+"/versionInfo",schemaPath:"#/properties/packages/items/properties/versionInfo/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err148];
}
else {
vErrors.push(err148);
}
errors++;
}
}
}
else {
const err149 = {instancePath:instancePath+"/packages/" + i8,schemaPath:"#/properties/packages/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err149];
}
else {
vErrors.push(err149);
}
errors++;
}
}
}
else {
const err150 = {instancePath:instancePath+"/packages",schemaPath:"#/properties/packages/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err150];
}
else {
vErrors.push(err150);
}
errors++;
}
}
if(data.files !== undefined){
let data98 = data.files;
if(Array.isArray(data98)){
const len16 = data98.length;
for(let i16=0; i16<len16; i16++){
let data99 = data98[i16];
if(data99 && typeof data99 == "object" && !Array.isArray(data99)){
if(data99.SPDXID === undefined){
const err151 = {instancePath:instancePath+"/files/" + i16,schemaPath:"#/properties/files/items/required",keyword:"required",params:{missingProperty: "SPDXID"},message:"must have required property '"+"SPDXID"+"'"};
if(vErrors === null){
vErrors = [err151];
}
else {
vErrors.push(err151);
}
errors++;
}
if(data99.checksums === undefined){
const err152 = {instancePath:instancePath+"/files/" + i16,schemaPath:"#/properties/files/items/required",keyword:"required",params:{missingProperty: "checksums"},message:"must have required property '"+"checksums"+"'"};
if(vErrors === null){
vErrors = [err152];
}
else {
vErrors.push(err152);
}
errors++;
}
if(data99.fileName === undefined){
const err153 = {instancePath:instancePath+"/files/" + i16,schemaPath:"#/properties/files/items/required",keyword:"required",params:{missingProperty: "fileName"},message:"must have required property '"+"fileName"+"'"};
if(vErrors === null){
vErrors = [err153];
}
else {
vErrors.push(err153);
}
errors++;
}
for(const key13 in data99){
if(!(func2.call(schema11.properties.files.items.properties, key13))){
const err154 = {instancePath:instancePath+"/files/" + i16,schemaPath:"#/properties/files/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key13},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err154];
}
else {
vErrors.push(err154);
}
errors++;
}
}
if(data99.SPDXID !== undefined){
if(typeof data99.SPDXID !== "string"){
const err155 = {instancePath:instancePath+"/files/" + i16+"/SPDXID",schemaPath:"#/properties/files/items/properties/SPDXID/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err155];
}
else {
vErrors.push(err155);
}
errors++;
}
}
if(data99.annotations !== undefined){
let data101 = data99.annotations;
if(Array.isArray(data101)){
const len17 = data101.length;
for(let i17=0; i17<len17; i17++){
let data102 = data101[i17];
if(data102 && typeof data102 == "object" && !Array.isArray(data102)){
if(data102.annotationDate === undefined){
const err156 = {instancePath:instancePath+"/files/" + i16+"/annotations/" + i17,schemaPath:"#/properties/files/items/properties/annotations/items/required",keyword:"required",params:{missingProperty: "annotationDate"},message:"must have required property '"+"annotationDate"+"'"};
if(vErrors === null){
vErrors = [err156];
}
else {
vErrors.push(err156);
}
errors++;
}
if(data102.annotationType === undefined){
const err157 = {instancePath:instancePath+"/files/" + i16+"/annotations/" + i17,schemaPath:"#/properties/files/items/properties/annotations/items/required",keyword:"required",params:{missingProperty: "annotationType"},message:"must have required property '"+"annotationType"+"'"};
if(vErrors === null){
vErrors = [err157];
}
else {
vErrors.push(err157);
}
errors++;
}
if(data102.annotator === undefined){
const err158 = {instancePath:instancePath+"/files/" + i16+"/annotations/" + i17,schemaPath:"#/properties/files/items/properties/annotations/items/required",keyword:"required",params:{missingProperty: "annotator"},message:"must have required property '"+"annotator"+"'"};
if(vErrors === null){
vErrors = [err158];
}
else {
vErrors.push(err158);
}
errors++;
}
if(data102.comment === undefined){
const err159 = {instancePath:instancePath+"/files/" + i16+"/annotations/" + i17,schemaPath:"#/properties/files/items/properties/annotations/items/required",keyword:"required",params:{missingProperty: "comment"},message:"must have required property '"+"comment"+"'"};
if(vErrors === null){
vErrors = [err159];
}
else {
vErrors.push(err159);
}
errors++;
}
for(const key14 in data102){
if(!((((key14 === "annotationDate") || (key14 === "annotationType")) || (key14 === "annotator")) || (key14 === "comment"))){
const err160 = {instancePath:instancePath+"/files/" + i16+"/annotations/" + i17,schemaPath:"#/properties/files/items/properties/annotations/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key14},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err160];
}
else {
vErrors.push(err160);
}
errors++;
}
}
if(data102.annotationDate !== undefined){
if(typeof data102.annotationDate !== "string"){
const err161 = {instancePath:instancePath+"/files/" + i16+"/annotations/" + i17+"/annotationDate",schemaPath:"#/properties/files/items/properties/annotations/items/properties/annotationDate/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err161];
}
else {
vErrors.push(err161);
}
errors++;
}
}
if(data102.annotationType !== undefined){
let data104 = data102.annotationType;
if(typeof data104 !== "string"){
const err162 = {instancePath:instancePath+"/files/" + i16+"/annotations/" + i17+"/annotationType",schemaPath:"#/properties/files/items/properties/annotations/items/properties/annotationType/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err162];
}
else {
vErrors.push(err162);
}
errors++;
}
if(!((data104 === "OTHER") || (data104 === "REVIEW"))){
const err163 = {instancePath:instancePath+"/files/" + i16+"/annotations/" + i17+"/annotationType",schemaPath:"#/properties/files/items/properties/annotations/items/properties/annotationType/enum",keyword:"enum",params:{allowedValues: schema11.properties.files.items.properties.annotations.items.properties.annotationType.enum},message:"must be equal to one of the allowed values"};
if(vErrors === null){
vErrors = [err163];
}
else {
vErrors.push(err163);
}
errors++;
}
}
if(data102.annotator !== undefined){
if(typeof data102.annotator !== "string"){
const err164 = {instancePath:instancePath+"/files/" + i16+"/annotations/" + i17+"/annotator",schemaPath:"#/properties/files/items/properties/annotations/items/properties/annotator/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err164];
}
else {
vErrors.push(err164);
}
errors++;
}
}
if(data102.comment !== undefined){
if(typeof data102.comment !== "string"){
const err165 = {instancePath:instancePath+"/files/" + i16+"/annotations/" + i17+"/comment",schemaPath:"#/properties/files/items/properties/annotations/items/properties/comment/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err165];
}
else {
vErrors.push(err165);
}
errors++;
}
}
}
else {
const err166 = {instancePath:instancePath+"/files/" + i16+"/annotations/" + i17,schemaPath:"#/properties/files/items/properties/annotations/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err166];
}
else {
vErrors.push(err166);
}
errors++;
}
}
}
else {
const err167 = {instancePath:instancePath+"/files/" + i16+"/annotations",schemaPath:"#/properties/files/items/properties/annotations/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err167];
}
else {
vErrors.push(err167);
}
errors++;
}
}
if(data99.artifactOfs !== undefined){
let data107 = data99.artifactOfs;
if(Array.isArray(data107)){
const len18 = data107.length;
for(let i18=0; i18<len18; i18++){
let data108 = data107[i18];
if(!(data108 && typeof data108 == "object" && !Array.isArray(data108))){
const err168 = {instancePath:instancePath+"/files/" + i16+"/artifactOfs/" + i18,schemaPath:"#/properties/files/items/properties/artifactOfs/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err168];
}
else {
vErrors.push(err168);
}
errors++;
}
}
}
else {
const err169 = {instancePath:instancePath+"/files/" + i16+"/artifactOfs",schemaPath:"#/properties/files/items/properties/artifactOfs/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err169];
}
else {
vErrors.push(err169);
}
errors++;
}
}
if(data99.attributionTexts !== undefined){
let data109 = data99.attributionTexts;
if(Array.isArray(data109)){
const len19 = data109.length;
for(let i19=0; i19<len19; i19++){
if(typeof data109[i19] !== "string"){
const err170 = {instancePath:instancePath+"/files/" + i16+"/attributionTexts/" + i19,schemaPath:"#/properties/files/items/properties/attributionTexts/items/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err170];
}
else {
vErrors.push(err170);
}
errors++;
}
}
}
else {
const err171 = {instancePath:instancePath+"/files/" + i16+"/attributionTexts",schemaPath:"#/properties/files/items/properties/attributionTexts/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err171];
}
else {
vErrors.push(err171);
}
errors++;
}
}
if(data99.checksums !== undefined){
let data111 = data99.checksums;
if(Array.isArray(data111)){
if(data111.length < 1){
const err172 = {instancePath:instancePath+"/files/" + i16+"/checksums",schemaPath:"#/properties/files/items/properties/checksums/minItems",keyword:"minItems",params:{limit: 1},message:"must NOT have fewer than 1 items"};
if(vErrors === null){
vErrors = [err172];
}
else {
vErrors.push(err172);
}
errors++;
}
const len20 = data111.length;
for(let i20=0; i20<len20; i20++){
let data112 = data111[i20];
if(data112 && typeof data112 == "object" && !Array.isArray(data112)){
if(data112.algorithm === undefined){
const err173 = {instancePath:instancePath+"/files/" + i16+"/checksums/" + i20,schemaPath:"#/properties/files/items/properties/checksums/items/required",keyword:"required",params:{missingProperty: "algorithm"},message:"must have required property '"+"algorithm"+"'"};
if(vErrors === null){
vErrors = [err173];
}
else {
vErrors.push(err173);
}
errors++;
}
if(data112.checksumValue === undefined){
const err174 = {instancePath:instancePath+"/files/" + i16+"/checksums/" + i20,schemaPath:"#/properties/files/items/properties/checksums/items/required",keyword:"required",params:{missingProperty: "checksumValue"},message:"must have required property '"+"checksumValue"+"'"};
if(vErrors === null){
vErrors = [err174];
}
else {
vErrors.push(err174);
}
errors++;
}
for(const key15 in data112){
if(!((key15 === "algorithm") || (key15 === "checksumValue"))){
const err175 = {instancePath:instancePath+"/files/" + i16+"/checksums/" + i20,schemaPath:"#/properties/files/items/properties/checksums/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key15},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err175];
}
else {
vErrors.push(err175);
}
errors++;
}
}
if(data112.algorithm !== undefined){
let data113 = data112.algorithm;
if(typeof data113 !== "string"){
const err176 = {instancePath:instancePath+"/files/" + i16+"/checksums/" + i20+"/algorithm",schemaPath:"#/properties/files/items/properties/checksums/items/properties/algorithm/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err176];
}
else {
vErrors.push(err176);
}
errors++;
}
if(!(((((((((((((((((data113 === "SHA1") || (data113 === "BLAKE3")) || (data113 === "SHA3-384")) || (data113 === "SHA256")) || (data113 === "SHA384")) || (data113 === "BLAKE2b-512")) || (data113 === "BLAKE2b-256")) || (data113 === "SHA3-512")) || (data113 === "MD2")) || (data113 === "ADLER32")) || (data113 === "MD4")) || (data113 === "SHA3-256")) || (data113 === "BLAKE2b-384")) || (data113 === "SHA512")) || (data113 === "MD6")) || (data113 === "MD5")) || (data113 === "SHA224"))){
const err177 = {instancePath:instancePath+"/files/" + i16+"/checksums/" + i20+"/algorithm",schemaPath:"#/properties/files/items/properties/checksums/items/properties/algorithm/enum",keyword:"enum",params:{allowedValues: schema11.properties.files.items.properties.checksums.items.properties.algorithm.enum},message:"must be equal to one of the allowed values"};
if(vErrors === null){
vErrors = [err177];
}
else {
vErrors.push(err177);
}
errors++;
}
}
if(data112.checksumValue !== undefined){
if(typeof data112.checksumValue !== "string"){
const err178 = {instancePath:instancePath+"/files/" + i16+"/checksums/" + i20+"/checksumValue",schemaPath:"#/properties/files/items/properties/checksums/items/properties/checksumValue/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err178];
}
else {
vErrors.push(err178);
}
errors++;
}
}
}
else {
const err179 = {instancePath:instancePath+"/files/" + i16+"/checksums/" + i20,schemaPath:"#/properties/files/items/properties/checksums/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err179];
}
else {
vErrors.push(err179);
}
errors++;
}
}
}
else {
const err180 = {instancePath:instancePath+"/files/" + i16+"/checksums",schemaPath:"#/properties/files/items/properties/checksums/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err180];
}
else {
vErrors.push(err180);
}
errors++;
}
}
if(data99.comment !== undefined){
if(typeof data99.comment !== "string"){
const err181 = {instancePath:instancePath+"/files/" + i16+"/comment",schemaPath:"#/properties/files/items/properties/comment/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err181];
}
else {
vErrors.push(err181);
}
errors++;
}
}
if(data99.copyrightText !== undefined){
if(typeof data99.copyrightText !== "string"){
const err182 = {instancePath:instancePath+"/files/" + i16+"/copyrightText",schemaPath:"#/properties/files/items/properties/copyrightText/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err182];
}
else {
vErrors.push(err182);
}
errors++;
}
}
if(data99.fileContributors !== undefined){
let data117 = data99.fileContributors;
if(Array.isArray(data117)){
const len21 = data117.length;
for(let i21=0; i21<len21; i21++){
if(typeof data117[i21] !== "string"){
const err183 = {instancePath:instancePath+"/files/" + i16+"/fileContributors/" + i21,schemaPath:"#/properties/files/items/properties/fileContributors/items/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err183];
}
else {
vErrors.push(err183);
}
errors++;
}
}
}
else {
const err184 = {instancePath:instancePath+"/files/" + i16+"/fileContributors",schemaPath:"#/properties/files/items/properties/fileContributors/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err184];
}
else {
vErrors.push(err184);
}
errors++;
}
}
if(data99.fileDependencies !== undefined){
let data119 = data99.fileDependencies;
if(Array.isArray(data119)){
const len22 = data119.length;
for(let i22=0; i22<len22; i22++){
if(typeof data119[i22] !== "string"){
const err185 = {instancePath:instancePath+"/files/" + i16+"/fileDependencies/" + i22,schemaPath:"#/properties/files/items/properties/fileDependencies/items/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err185];
}
else {
vErrors.push(err185);
}
errors++;
}
}
}
else {
const err186 = {instancePath:instancePath+"/files/" + i16+"/fileDependencies",schemaPath:"#/properties/files/items/properties/fileDependencies/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err186];
}
else {
vErrors.push(err186);
}
errors++;
}
}
if(data99.fileName !== undefined){
if(typeof data99.fileName !== "string"){
const err187 = {instancePath:instancePath+"/files/" + i16+"/fileName",schemaPath:"#/properties/files/items/properties/fileName/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err187];
}
else {
vErrors.push(err187);
}
errors++;
}
}
if(data99.fileTypes !== undefined){
let data122 = data99.fileTypes;
if(Array.isArray(data122)){
const len23 = data122.length;
for(let i23=0; i23<len23; i23++){
let data123 = data122[i23];
if(typeof data123 !== "string"){
const err188 = {instancePath:instancePath+"/files/" + i16+"/fileTypes/" + i23,schemaPath:"#/properties/files/items/properties/fileTypes/items/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err188];
}
else {
vErrors.push(err188);
}
errors++;
}
if(!(((((((((((data123 === "OTHER") || (data123 === "DOCUMENTATION")) || (data123 === "IMAGE")) || (data123 === "VIDEO")) || (data123 === "ARCHIVE")) || (data123 === "SPDX")) || (data123 === "APPLICATION")) || (data123 === "SOURCE")) || (data123 === "BINARY")) || (data123 === "TEXT")) || (data123 === "AUDIO"))){
const err189 = {instancePath:instancePath+"/files/" + i16+"/fileTypes/" + i23,schemaPath:"#/properties/files/items/properties/fileTypes/items/enum",keyword:"enum",params:{allowedValues: schema11.properties.files.items.properties.fileTypes.items.enum},message:"must be equal to one of the allowed values"};
if(vErrors === null){
vErrors = [err189];
}
else {
vErrors.push(err189);
}
errors++;
}
}
}
else {
const err190 = {instancePath:instancePath+"/files/" + i16+"/fileTypes",schemaPath:"#/properties/files/items/properties/fileTypes/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err190];
}
else {
vErrors.push(err190);
}
errors++;
}
}
if(data99.licenseComments !== undefined){
if(typeof data99.licenseComments !== "string"){
const err191 = {instancePath:instancePath+"/files/" + i16+"/licenseComments",schemaPath:"#/properties/files/items/properties/licenseComments/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err191];
}
else {
vErrors.push(err191);
}
errors++;
}
}
if(data99.licenseConcluded !== undefined){
if(typeof data99.licenseConcluded !== "string"){
const err192 = {instancePath:instancePath+"/files/" + i16+"/licenseConcluded",schemaPath:"#/properties/files/items/properties/licenseConcluded/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err192];
}
else {
vErrors.push(err192);
}
errors++;
}
}
if(data99.licenseInfoInFiles !== undefined){
let data126 = data99.licenseInfoInFiles;
if(Array.isArray(data126)){
const len24 = data126.length;
for(let i24=0; i24<len24; i24++){
if(typeof data126[i24] !== "string"){
const err193 = {instancePath:instancePath+"/files/" + i16+"/licenseInfoInFiles/" + i24,schemaPath:"#/properties/files/items/properties/licenseInfoInFiles/items/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err193];
}
else {
vErrors.push(err193);
}
errors++;
}
}
}
else {
const err194 = {instancePath:instancePath+"/files/" + i16+"/licenseInfoInFiles",schemaPath:"#/properties/files/items/properties/licenseInfoInFiles/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err194];
}
else {
vErrors.push(err194);
}
errors++;
}
}
if(data99.noticeText !== undefined){
if(typeof data99.noticeText !== "string"){
const err195 = {instancePath:instancePath+"/files/" + i16+"/noticeText",schemaPath:"#/properties/files/items/properties/noticeText/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err195];
}
else {
vErrors.push(err195);
}
errors++;
}
}
}
else {
const err196 = {instancePath:instancePath+"/files/" + i16,schemaPath:"#/properties/files/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err196];
}
else {
vErrors.push(err196);
}
errors++;
}
}
}
else {
const err197 = {instancePath:instancePath+"/files",schemaPath:"#/properties/files/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err197];
}
else {
vErrors.push(err197);
}
errors++;
}
}
if(data.snippets !== undefined){
let data129 = data.snippets;
if(Array.isArray(data129)){
const len25 = data129.length;
for(let i25=0; i25<len25; i25++){
let data130 = data129[i25];
if(data130 && typeof data130 == "object" && !Array.isArray(data130)){
if(data130.SPDXID === undefined){
const err198 = {instancePath:instancePath+"/snippets/" + i25,schemaPath:"#/properties/snippets/items/required",keyword:"required",params:{missingProperty: "SPDXID"},message:"must have required property '"+"SPDXID"+"'"};
if(vErrors === null){
vErrors = [err198];
}
else {
vErrors.push(err198);
}
errors++;
}
if(data130.name === undefined){
const err199 = {instancePath:instancePath+"/snippets/" + i25,schemaPath:"#/properties/snippets/items/required",keyword:"required",params:{missingProperty: "name"},message:"must have required property '"+"name"+"'"};
if(vErrors === null){
vErrors = [err199];
}
else {
vErrors.push(err199);
}
errors++;
}
if(data130.ranges === undefined){
const err200 = {instancePath:instancePath+"/snippets/" + i25,schemaPath:"#/properties/snippets/items/required",keyword:"required",params:{missingProperty: "ranges"},message:"must have required property '"+"ranges"+"'"};
if(vErrors === null){
vErrors = [err200];
}
else {
vErrors.push(err200);
}
errors++;
}
if(data130.snippetFromFile === undefined){
const err201 = {instancePath:instancePath+"/snippets/" + i25,schemaPath:"#/properties/snippets/items/required",keyword:"required",params:{missingProperty: "snippetFromFile"},message:"must have required property '"+"snippetFromFile"+"'"};
if(vErrors === null){
vErrors = [err201];
}
else {
vErrors.push(err201);
}
errors++;
}
for(const key16 in data130){
if(!(func2.call(schema11.properties.snippets.items.properties, key16))){
const err202 = {instancePath:instancePath+"/snippets/" + i25,schemaPath:"#/properties/snippets/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key16},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err202];
}
else {
vErrors.push(err202);
}
errors++;
}
}
if(data130.SPDXID !== undefined){
if(typeof data130.SPDXID !== "string"){
const err203 = {instancePath:instancePath+"/snippets/" + i25+"/SPDXID",schemaPath:"#/properties/snippets/items/properties/SPDXID/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err203];
}
else {
vErrors.push(err203);
}
errors++;
}
}
if(data130.annotations !== undefined){
let data132 = data130.annotations;
if(Array.isArray(data132)){
const len26 = data132.length;
for(let i26=0; i26<len26; i26++){
let data133 = data132[i26];
if(data133 && typeof data133 == "object" && !Array.isArray(data133)){
if(data133.annotationDate === undefined){
const err204 = {instancePath:instancePath+"/snippets/" + i25+"/annotations/" + i26,schemaPath:"#/properties/snippets/items/properties/annotations/items/required",keyword:"required",params:{missingProperty: "annotationDate"},message:"must have required property '"+"annotationDate"+"'"};
if(vErrors === null){
vErrors = [err204];
}
else {
vErrors.push(err204);
}
errors++;
}
if(data133.annotationType === undefined){
const err205 = {instancePath:instancePath+"/snippets/" + i25+"/annotations/" + i26,schemaPath:"#/properties/snippets/items/properties/annotations/items/required",keyword:"required",params:{missingProperty: "annotationType"},message:"must have required property '"+"annotationType"+"'"};
if(vErrors === null){
vErrors = [err205];
}
else {
vErrors.push(err205);
}
errors++;
}
if(data133.annotator === undefined){
const err206 = {instancePath:instancePath+"/snippets/" + i25+"/annotations/" + i26,schemaPath:"#/properties/snippets/items/properties/annotations/items/required",keyword:"required",params:{missingProperty: "annotator"},message:"must have required property '"+"annotator"+"'"};
if(vErrors === null){
vErrors = [err206];
}
else {
vErrors.push(err206);
}
errors++;
}
if(data133.comment === undefined){
const err207 = {instancePath:instancePath+"/snippets/" + i25+"/annotations/" + i26,schemaPath:"#/properties/snippets/items/properties/annotations/items/required",keyword:"required",params:{missingProperty: "comment"},message:"must have required property '"+"comment"+"'"};
if(vErrors === null){
vErrors = [err207];
}
else {
vErrors.push(err207);
}
errors++;
}
for(const key17 in data133){
if(!((((key17 === "annotationDate") || (key17 === "annotationType")) || (key17 === "annotator")) || (key17 === "comment"))){
const err208 = {instancePath:instancePath+"/snippets/" + i25+"/annotations/" + i26,schemaPath:"#/properties/snippets/items/properties/annotations/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key17},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err208];
}
else {
vErrors.push(err208);
}
errors++;
}
}
if(data133.annotationDate !== undefined){
if(typeof data133.annotationDate !== "string"){
const err209 = {instancePath:instancePath+"/snippets/" + i25+"/annotations/" + i26+"/annotationDate",schemaPath:"#/properties/snippets/items/properties/annotations/items/properties/annotationDate/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err209];
}
else {
vErrors.push(err209);
}
errors++;
}
}
if(data133.annotationType !== undefined){
let data135 = data133.annotationType;
if(typeof data135 !== "string"){
const err210 = {instancePath:instancePath+"/snippets/" + i25+"/annotations/" + i26+"/annotationType",schemaPath:"#/properties/snippets/items/properties/annotations/items/properties/annotationType/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err210];
}
else {
vErrors.push(err210);
}
errors++;
}
if(!((data135 === "OTHER") || (data135 === "REVIEW"))){
const err211 = {instancePath:instancePath+"/snippets/" + i25+"/annotations/" + i26+"/annotationType",schemaPath:"#/properties/snippets/items/properties/annotations/items/properties/annotationType/enum",keyword:"enum",params:{allowedValues: schema11.properties.snippets.items.properties.annotations.items.properties.annotationType.enum},message:"must be equal to one of the allowed values"};
if(vErrors === null){
vErrors = [err211];
}
else {
vErrors.push(err211);
}
errors++;
}
}
if(data133.annotator !== undefined){
if(typeof data133.annotator !== "string"){
const err212 = {instancePath:instancePath+"/snippets/" + i25+"/annotations/" + i26+"/annotator",schemaPath:"#/properties/snippets/items/properties/annotations/items/properties/annotator/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err212];
}
else {
vErrors.push(err212);
}
errors++;
}
}
if(data133.comment !== undefined){
if(typeof data133.comment !== "string"){
const err213 = {instancePath:instancePath+"/snippets/" + i25+"/annotations/" + i26+"/comment",schemaPath:"#/properties/snippets/items/properties/annotations/items/properties/comment/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err213];
}
else {
vErrors.push(err213);
}
errors++;
}
}
}
else {
const err214 = {instancePath:instancePath+"/snippets/" + i25+"/annotations/" + i26,schemaPath:"#/properties/snippets/items/properties/annotations/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err214];
}
else {
vErrors.push(err214);
}
errors++;
}
}
}
else {
const err215 = {instancePath:instancePath+"/snippets/" + i25+"/annotations",schemaPath:"#/properties/snippets/items/properties/annotations/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err215];
}
else {
vErrors.push(err215);
}
errors++;
}
}
if(data130.attributionTexts !== undefined){
let data138 = data130.attributionTexts;
if(Array.isArray(data138)){
const len27 = data138.length;
for(let i27=0; i27<len27; i27++){
if(typeof data138[i27] !== "string"){
const err216 = {instancePath:instancePath+"/snippets/" + i25+"/attributionTexts/" + i27,schemaPath:"#/properties/snippets/items/properties/attributionTexts/items/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err216];
}
else {
vErrors.push(err216);
}
errors++;
}
}
}
else {
const err217 = {instancePath:instancePath+"/snippets/" + i25+"/attributionTexts",schemaPath:"#/properties/snippets/items/properties/attributionTexts/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err217];
}
else {
vErrors.push(err217);
}
errors++;
}
}
if(data130.comment !== undefined){
if(typeof data130.comment !== "string"){
const err218 = {instancePath:instancePath+"/snippets/" + i25+"/comment",schemaPath:"#/properties/snippets/items/properties/comment/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err218];
}
else {
vErrors.push(err218);
}
errors++;
}
}
if(data130.copyrightText !== undefined){
if(typeof data130.copyrightText !== "string"){
const err219 = {instancePath:instancePath+"/snippets/" + i25+"/copyrightText",schemaPath:"#/properties/snippets/items/properties/copyrightText/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err219];
}
else {
vErrors.push(err219);
}
errors++;
}
}
if(data130.licenseComments !== undefined){
if(typeof data130.licenseComments !== "string"){
const err220 = {instancePath:instancePath+"/snippets/" + i25+"/licenseComments",schemaPath:"#/properties/snippets/items/properties/licenseComments/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err220];
}
else {
vErrors.push(err220);
}
errors++;
}
}
if(data130.licenseConcluded !== undefined){
if(typeof data130.licenseConcluded !== "string"){
const err221 = {instancePath:instancePath+"/snippets/" + i25+"/licenseConcluded",schemaPath:"#/properties/snippets/items/properties/licenseConcluded/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err221];
}
else {
vErrors.push(err221);
}
errors++;
}
}
if(data130.licenseInfoInSnippets !== undefined){
let data144 = data130.licenseInfoInSnippets;
if(Array.isArray(data144)){
const len28 = data144.length;
for(let i28=0; i28<len28; i28++){
if(typeof data144[i28] !== "string"){
const err222 = {instancePath:instancePath+"/snippets/" + i25+"/licenseInfoInSnippets/" + i28,schemaPath:"#/properties/snippets/items/properties/licenseInfoInSnippets/items/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err222];
}
else {
vErrors.push(err222);
}
errors++;
}
}
}
else {
const err223 = {instancePath:instancePath+"/snippets/" + i25+"/licenseInfoInSnippets",schemaPath:"#/properties/snippets/items/properties/licenseInfoInSnippets/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err223];
}
else {
vErrors.push(err223);
}
errors++;
}
}
if(data130.name !== undefined){
if(typeof data130.name !== "string"){
const err224 = {instancePath:instancePath+"/snippets/" + i25+"/name",schemaPath:"#/properties/snippets/items/properties/name/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err224];
}
else {
vErrors.push(err224);
}
errors++;
}
}
if(data130.ranges !== undefined){
let data147 = data130.ranges;
if(Array.isArray(data147)){
if(data147.length < 1){
const err225 = {instancePath:instancePath+"/snippets/" + i25+"/ranges",schemaPath:"#/properties/snippets/items/properties/ranges/minItems",keyword:"minItems",params:{limit: 1},message:"must NOT have fewer than 1 items"};
if(vErrors === null){
vErrors = [err225];
}
else {
vErrors.push(err225);
}
errors++;
}
const len29 = data147.length;
for(let i29=0; i29<len29; i29++){
let data148 = data147[i29];
if(data148 && typeof data148 == "object" && !Array.isArray(data148)){
if(data148.endPointer === undefined){
const err226 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29,schemaPath:"#/properties/snippets/items/properties/ranges/items/required",keyword:"required",params:{missingProperty: "endPointer"},message:"must have required property '"+"endPointer"+"'"};
if(vErrors === null){
vErrors = [err226];
}
else {
vErrors.push(err226);
}
errors++;
}
if(data148.startPointer === undefined){
const err227 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29,schemaPath:"#/properties/snippets/items/properties/ranges/items/required",keyword:"required",params:{missingProperty: "startPointer"},message:"must have required property '"+"startPointer"+"'"};
if(vErrors === null){
vErrors = [err227];
}
else {
vErrors.push(err227);
}
errors++;
}
for(const key18 in data148){
if(!((key18 === "endPointer") || (key18 === "startPointer"))){
const err228 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29,schemaPath:"#/properties/snippets/items/properties/ranges/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key18},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err228];
}
else {
vErrors.push(err228);
}
errors++;
}
}
if(data148.endPointer !== undefined){
let data149 = data148.endPointer;
if(data149 && typeof data149 == "object" && !Array.isArray(data149)){
if(data149.reference === undefined){
const err229 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29+"/endPointer",schemaPath:"#/properties/snippets/items/properties/ranges/items/properties/endPointer/required",keyword:"required",params:{missingProperty: "reference"},message:"must have required property '"+"reference"+"'"};
if(vErrors === null){
vErrors = [err229];
}
else {
vErrors.push(err229);
}
errors++;
}
for(const key19 in data149){
if(!(((key19 === "reference") || (key19 === "offset")) || (key19 === "lineNumber"))){
const err230 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29+"/endPointer",schemaPath:"#/properties/snippets/items/properties/ranges/items/properties/endPointer/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key19},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err230];
}
else {
vErrors.push(err230);
}
errors++;
}
}
if(data149.reference !== undefined){
if(typeof data149.reference !== "string"){
const err231 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29+"/endPointer/reference",schemaPath:"#/properties/snippets/items/properties/ranges/items/properties/endPointer/properties/reference/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err231];
}
else {
vErrors.push(err231);
}
errors++;
}
}
if(data149.offset !== undefined){
let data151 = data149.offset;
if(!((typeof data151 == "number") && (!(data151 % 1) && !isNaN(data151)))){
const err232 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29+"/endPointer/offset",schemaPath:"#/properties/snippets/items/properties/ranges/items/properties/endPointer/properties/offset/type",keyword:"type",params:{type: "integer"},message:"must be integer"};
if(vErrors === null){
vErrors = [err232];
}
else {
vErrors.push(err232);
}
errors++;
}
}
if(data149.lineNumber !== undefined){
let data152 = data149.lineNumber;
if(!((typeof data152 == "number") && (!(data152 % 1) && !isNaN(data152)))){
const err233 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29+"/endPointer/lineNumber",schemaPath:"#/properties/snippets/items/properties/ranges/items/properties/endPointer/properties/lineNumber/type",keyword:"type",params:{type: "integer"},message:"must be integer"};
if(vErrors === null){
vErrors = [err233];
}
else {
vErrors.push(err233);
}
errors++;
}
}
}
else {
const err234 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29+"/endPointer",schemaPath:"#/properties/snippets/items/properties/ranges/items/properties/endPointer/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err234];
}
else {
vErrors.push(err234);
}
errors++;
}
}
if(data148.startPointer !== undefined){
let data153 = data148.startPointer;
if(data153 && typeof data153 == "object" && !Array.isArray(data153)){
if(data153.reference === undefined){
const err235 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29+"/startPointer",schemaPath:"#/properties/snippets/items/properties/ranges/items/properties/startPointer/required",keyword:"required",params:{missingProperty: "reference"},message:"must have required property '"+"reference"+"'"};
if(vErrors === null){
vErrors = [err235];
}
else {
vErrors.push(err235);
}
errors++;
}
for(const key20 in data153){
if(!(((key20 === "reference") || (key20 === "offset")) || (key20 === "lineNumber"))){
const err236 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29+"/startPointer",schemaPath:"#/properties/snippets/items/properties/ranges/items/properties/startPointer/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key20},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err236];
}
else {
vErrors.push(err236);
}
errors++;
}
}
if(data153.reference !== undefined){
if(typeof data153.reference !== "string"){
const err237 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29+"/startPointer/reference",schemaPath:"#/properties/snippets/items/properties/ranges/items/properties/startPointer/properties/reference/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err237];
}
else {
vErrors.push(err237);
}
errors++;
}
}
if(data153.offset !== undefined){
let data155 = data153.offset;
if(!((typeof data155 == "number") && (!(data155 % 1) && !isNaN(data155)))){
const err238 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29+"/startPointer/offset",schemaPath:"#/properties/snippets/items/properties/ranges/items/properties/startPointer/properties/offset/type",keyword:"type",params:{type: "integer"},message:"must be integer"};
if(vErrors === null){
vErrors = [err238];
}
else {
vErrors.push(err238);
}
errors++;
}
}
if(data153.lineNumber !== undefined){
let data156 = data153.lineNumber;
if(!((typeof data156 == "number") && (!(data156 % 1) && !isNaN(data156)))){
const err239 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29+"/startPointer/lineNumber",schemaPath:"#/properties/snippets/items/properties/ranges/items/properties/startPointer/properties/lineNumber/type",keyword:"type",params:{type: "integer"},message:"must be integer"};
if(vErrors === null){
vErrors = [err239];
}
else {
vErrors.push(err239);
}
errors++;
}
}
}
else {
const err240 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29+"/startPointer",schemaPath:"#/properties/snippets/items/properties/ranges/items/properties/startPointer/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err240];
}
else {
vErrors.push(err240);
}
errors++;
}
}
}
else {
const err241 = {instancePath:instancePath+"/snippets/" + i25+"/ranges/" + i29,schemaPath:"#/properties/snippets/items/properties/ranges/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err241];
}
else {
vErrors.push(err241);
}
errors++;
}
}
}
else {
const err242 = {instancePath:instancePath+"/snippets/" + i25+"/ranges",schemaPath:"#/properties/snippets/items/properties/ranges/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err242];
}
else {
vErrors.push(err242);
}
errors++;
}
}
if(data130.snippetFromFile !== undefined){
if(typeof data130.snippetFromFile !== "string"){
const err243 = {instancePath:instancePath+"/snippets/" + i25+"/snippetFromFile",schemaPath:"#/properties/snippets/items/properties/snippetFromFile/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err243];
}
else {
vErrors.push(err243);
}
errors++;
}
}
}
else {
const err244 = {instancePath:instancePath+"/snippets/" + i25,schemaPath:"#/properties/snippets/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err244];
}
else {
vErrors.push(err244);
}
errors++;
}
}
}
else {
const err245 = {instancePath:instancePath+"/snippets",schemaPath:"#/properties/snippets/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err245];
}
else {
vErrors.push(err245);
}
errors++;
}
}
if(data.relationships !== undefined){
let data158 = data.relationships;
if(Array.isArray(data158)){
const len30 = data158.length;
for(let i30=0; i30<len30; i30++){
let data159 = data158[i30];
if(data159 && typeof data159 == "object" && !Array.isArray(data159)){
if(data159.spdxElementId === undefined){
const err246 = {instancePath:instancePath+"/relationships/" + i30,schemaPath:"#/properties/relationships/items/required",keyword:"required",params:{missingProperty: "spdxElementId"},message:"must have required property '"+"spdxElementId"+"'"};
if(vErrors === null){
vErrors = [err246];
}
else {
vErrors.push(err246);
}
errors++;
}
if(data159.relatedSpdxElement === undefined){
const err247 = {instancePath:instancePath+"/relationships/" + i30,schemaPath:"#/properties/relationships/items/required",keyword:"required",params:{missingProperty: "relatedSpdxElement"},message:"must have required property '"+"relatedSpdxElement"+"'"};
if(vErrors === null){
vErrors = [err247];
}
else {
vErrors.push(err247);
}
errors++;
}
if(data159.relationshipType === undefined){
const err248 = {instancePath:instancePath+"/relationships/" + i30,schemaPath:"#/properties/relationships/items/required",keyword:"required",params:{missingProperty: "relationshipType"},message:"must have required property '"+"relationshipType"+"'"};
if(vErrors === null){
vErrors = [err248];
}
else {
vErrors.push(err248);
}
errors++;
}
for(const key21 in data159){
if(!((((key21 === "spdxElementId") || (key21 === "comment")) || (key21 === "relatedSpdxElement")) || (key21 === "relationshipType"))){
const err249 = {instancePath:instancePath+"/relationships/" + i30,schemaPath:"#/properties/relationships/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key21},message:"must NOT have additional properties"};
if(vErrors === null){
vErrors = [err249];
}
else {
vErrors.push(err249);
}
errors++;
}
}
if(data159.spdxElementId !== undefined){
if(typeof data159.spdxElementId !== "string"){
const err250 = {instancePath:instancePath+"/relationships/" + i30+"/spdxElementId",schemaPath:"#/properties/relationships/items/properties/spdxElementId/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err250];
}
else {
vErrors.push(err250);
}
errors++;
}
}
if(data159.comment !== undefined){
if(typeof data159.comment !== "string"){
const err251 = {instancePath:instancePath+"/relationships/" + i30+"/comment",schemaPath:"#/properties/relationships/items/properties/comment/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err251];
}
else {
vErrors.push(err251);
}
errors++;
}
}
if(data159.relatedSpdxElement !== undefined){
if(typeof data159.relatedSpdxElement !== "string"){
const err252 = {instancePath:instancePath+"/relationships/" + i30+"/relatedSpdxElement",schemaPath:"#/properties/relationships/items/properties/relatedSpdxElement/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err252];
}
else {
vErrors.push(err252);
}
errors++;
}
}
if(data159.relationshipType !== undefined){
let data163 = data159.relationshipType;
if(typeof data163 !== "string"){
const err253 = {instancePath:instancePath+"/relationships/" + i30+"/relationshipType",schemaPath:"#/properties/relationships/items/properties/relationshipType/type",keyword:"type",params:{type: "string"},message:"must be string"};
if(vErrors === null){
vErrors = [err253];
}
else {
vErrors.push(err253);
}
errors++;
}
if(!(((((((((((((((((((((((((((((((((((((((((((((data163 === "VARIANT_OF") || (data163 === "COPY_OF")) || (data163 === "PATCH_FOR")) || (data163 === "TEST_DEPENDENCY_OF")) || (data163 === "CONTAINED_BY")) || (data163 === "DATA_FILE_OF")) || (data163 === "OPTIONAL_COMPONENT_OF")) || (data163 === "ANCESTOR_OF")) || (data163 === "GENERATES")) || (data163 === "CONTAINS")) || (data163 === "OPTIONAL_DEPENDENCY_OF")) || (data163 === "FILE_ADDED")) || (data163 === "REQUIREMENT_DESCRIPTION_FOR")) || (data163 === "DEV_DEPENDENCY_OF")) || (data163 === "DEPENDENCY_OF")) || (data163 === "BUILD_DEPENDENCY_OF")) || (data163 === "DESCRIBES")) || (data163 === "PREREQUISITE_FOR")) || (data163 === "HAS_PREREQUISITE")) || (data163 === "PROVIDED_DEPENDENCY_OF")) || (data163 === "DYNAMIC_LINK")) || (data163 === "DESCRIBED_BY")) || (data163 === "METAFILE_OF")) || (data163 === "DEPENDENCY_MANIFEST_OF")) || (data163 === "PATCH_APPLIED")) || (data163 === "RUNTIME_DEPENDENCY_OF")) || (data163 === "TEST_OF")) || (data163 === "TEST_TOOL_OF")) || (data163 === "DEPENDS_ON")) || (data163 === "SPECIFICATION_FOR")) || (data163 === "FILE_MODIFIED")) || (data163 === "DISTRIBUTION_ARTIFACT")) || (data163 === "AMENDS")) || (data163 === "DOCUMENTATION_OF")) || (data163 === "GENERATED_FROM")) || (data163 === "STATIC_LINK")) || (data163 === "OTHER")) || (data163 === "BUILD_TOOL_OF")) || (data163 === "TEST_CASE_OF")) || (data163 === "PACKAGE_OF")) || (data163 === "DESCENDANT_OF")) || (data163 === "FILE_DELETED")) || (data163 === "EXPANDED_FROM_ARCHIVE")) || (data163 === "DEV_TOOL_OF")) || (data163 === "EXAMPLE_OF"))){
const err254 = {instancePath:instancePath+"/relationships/" + i30+"/relationshipType",schemaPath:"#/properties/relationships/items/properties/relationshipType/enum",keyword:"enum",params:{allowedValues: schema11.properties.relationships.items.properties.relationshipType.enum},message:"must be equal to one of the allowed values"};
if(vErrors === null){
vErrors = [err254];
}
else {
vErrors.push(err254);
}
errors++;
}
}
}
else {
const err255 = {instancePath:instancePath+"/relationships/" + i30,schemaPath:"#/properties/relationships/items/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err255];
}
else {
vErrors.push(err255);
}
errors++;
}
}
}
else {
const err256 = {instancePath:instancePath+"/relationships",schemaPath:"#/properties/relationships/type",keyword:"type",params:{type: "array"},message:"must be array"};
if(vErrors === null){
vErrors = [err256];
}
else {
vErrors.push(err256);
}
errors++;
}
}
}
else {
const err257 = {instancePath,schemaPath:"#/type",keyword:"type",params:{type: "object"},message:"must be object"};
if(vErrors === null){
vErrors = [err257];
}
else {
vErrors.push(err257);
}
errors++;
}
validate10.errors = vErrors;
return errors === 0;
}
