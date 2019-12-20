export const firstPage = {
  data: [
    {
      "id": "aaa",
      "name": "alpha@google.com",
      "ts": 1572000000000,
      "changeCount": 2,
      "details": [
        {
          "test_name": "async_rescale_and_read_dog_up",
          "digest": "f16298eb14e19f9230fe81615200561f",
          "label": "positive"
        }, {
          "test_name": "async_rescale_and_read_rose",
          "digest": "35c77280a7d5378033f9bf8f3c755e78",
          "label": "positive"
        },
      ],
    }, {
      "id": "bbb",
      "name": "beta@google.com",
      "ts": 1571900000000,
      "changeCount": 1,
      "details": [{
        "test_name": "draw_image_set",
        "digest": "b788aadee662c2b0390d698cbe68b808",
        "label": "positive"
      }],
    }, {
      "id": "ccc",
      "name": "gamma@google.com",
      "ts": 1571800000000,
      "changeCount": 1,
      "details": [{
        "test_name": "filterbitmap_text_7.00pt",
        "digest": "454b4b547bc6ceb4cdeb3305553be98a",
        "label": "positive"
      }],
    },
  ],
  status: 200,
  pagination: {
    offset: 0,
    size: 3,
    total: 9,
  },
};

export const firstPageAfterUndoingFirstEntry = {
  data: [
    {
      "id": "bbb",
      "name": "beta@google.com",
      "ts": 1571900000000,
      "changeCount": 1,
      "details": [{
        "test_name": "draw_image_set",
        "digest": "b788aadee662c2b0390d698cbe68b808",
        "label": "positive"
      }],
    }, {
      "id": "ccc",
      "name": "gamma@google.com",
      "ts": 1571800000000,
      "changeCount": 1,
      "details": [{
        "test_name": "filterbitmap_text_7.00pt",
        "digest": "454b4b547bc6ceb4cdeb3305553be98a",
        "label": "positive"
      }],
    }, {
      "id": "ddd",
      "name": "delta@google.com",
      "ts": 1571700000000,
      "changeCount": 1,
      "details": [{
        "test_name": "filterbitmap_text_10.00pt",
        "digest": "fc8392000945e68334c5ccd333b201b3",
        "label": "positive"
      }],
    },
  ],
  status: 200,
  pagination: {
    offset: 0,
    size: 3,
    total: 9,
  },
};

// Returned by /json/triagelog/undo. We never show this in the UI, but we
// simulate this response anyway to test that it is ignored by the page.
export const firstPageWithoutDetailsAfterUndoingFirstEntry = {
  data: [
    {
      "id": "bbb",
      "name": "beta@google.com",
      "ts": 1571900000000,
      "changeCount": 1,
    }, {
      "id": "ccc",
      "name": "gamma@google.com",
      "ts": 1571800000000,
      "changeCount": 1,
    }, {
      "id": "ddd",
      "name": "delta@google.com",
      "ts": 1571700000000,
      "changeCount": 1,
    },
  ],
  status: 200,
  pagination: {
    offset: 0,
    size: 3,
    total: 5,
  },
};

export const secondPage = {
  data: [
    {
      "id": "ddd",
      "name": "delta@google.com",
      "ts": 1571700000000,
      "changeCount": 1,
      "details": [{
        "test_name": "filterbitmap_text_10.00pt",
        "digest": "fc8392000945e68334c5ccd333b201b3",
        "label": "positive"
      }],
    }, {
      "id": "eee",
      "name": "epsilon@google.com",
      "ts": 1571600000000,
      "changeCount": 1,
      "details": [{
        "test_name": "filterbitmap_image_mandrill_32.png",
        "digest": "7606bfd486f7dfdf299d9d9da8f99c8e",
        "label": "positive"
      }],
    }, {
      "id": "fff",
      "name": "zeta@google.com",
      "ts": 1571500000000,
      "changeCount": 1,
      "details": [{
        "test_name": "drawminibitmaprect_aa",
        "digest": "95e1b42fcaaff5d0d08b4ed465d79437",
        "label": "positive"
      }],
    },
  ],
  status: 200,
  pagination: {
    offset: 3,
    size: 3,
    total: 9,
  },
};

export const thirdPage = {
  data: [
    {
      "id": "ggg",
      "name": "eta@google.com",
      "ts": 1571400000000,
      "changeCount": 1,
      "details": [{
        "test_name": "colorcomposefilter_wacky",
        "digest": "68e41c7f7d91f432fd36d71fe1249443",
        "label": "positive"
      }],
    }, {
      "id": "hhh",
      "name": "theta@google.com",
      "ts": 1571300000000,
      "changeCount": 1,
      "details": [{
        "test_name": "circular_arc_stroke_matrix",
        "digest": "c482098318879e7d2cf4f0414b607156",
        "label": "positive"
      }],
    }, {
      "id": "iii",
      "name": "iota@google.com",
      "ts": 1571200000000,
      "changeCount": 1,
      "details": [{
        "test_name": "dftext_blob_persp",
        "digest": "a41baae99abd37d9ed606e8bc27df6a2",
        "label": "positive"
      }],
    },
  ],
  status: 200,
  pagination: {
    offset: 3,
    size: 3,
    total: 9,
  },
};