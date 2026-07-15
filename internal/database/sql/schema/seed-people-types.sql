INSERT INTO people_type (name, description)
VALUES
    ('author', 'Wrote or created the document'),
    ('sender', 'Dispatched or submitted the document'),
    ('recipient', 'Received or was the addressee of the document'),
    ('signatory', 'Signed or executed the document'),
    ('correspondent', 'General party in a correspondence exchange'),
    ('subject', 'Person the document is primarily about or references'),
    ('contact', 'Administrative or business contact person'),
    ('client', 'Customer, patient, or client referenced in the document'),
    ('supplier', 'Vendor, contractor, or service provider'),
    ('employee', 'Staff or employee mentioned in an official capacity'),
    ('legal_representative', 'Lawyer, attorney, notary, or authorized agent'),
    ('beneficiary', 'Beneficiary, assignee, heir, or payee'),
    ('witness', 'Witnessed the signing or execution'),
    ('other', 'Relationship not covered by any other type'),
    ('unknown', 'Relationship cannot be determined')
ON CONFLICT (name) DO NOTHING;
