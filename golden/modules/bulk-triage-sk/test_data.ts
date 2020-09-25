import { Label, TriageRequestData, TriageRequest } from '../rpc_types';

export const examplePageData: TriageRequestData = {
  alpha_test: {
    aaaaaaaaaaaaaaaaaaaaaaaaaaa: 'positive',
    bbbbbbbbbbbbbbbbbbbbbbbbbbb: 'negative',
  },

  beta_test: {
    ccccccccccccccccccccccccccc: 'positive',
  },
};

export const expectedPageDataTriageRequest: TriageRequest = {
  testDigestStatus: {
    alpha_test: {
      aaaaaaaaaaaaaaaaaaaaaaaaaaa: 'positive',
      bbbbbbbbbbbbbbbbbbbbbbbbbbb: 'negative',
    },
    beta_test: {
      ccccccccccccccccccccccccccc: 'positive',
    }
  },
  changelist_id: '',
  crs: '',
};

export const exampleAllData: TriageRequestData = {
  alpha_test: {
    aaaaaaaaaaaaaaaaaaaaaaaaaaa: 'positive',
    bbbbbbbbbbbbbbbbbbbbbbbbbbb: 'negative',
    ddddddddddddddddddddddddddd: 'positive',
  },

  beta_test: {
    ccccccccccccccccccccccccccc: 'positive',
    ddddddddddddddddddddddddddd: 'negative',
  },

  gamma_test: {
    eeeeeeeeeeeeeeeeeeeeeeeeeee: '' as Label, // pretend this has no closest reference image.
  },
};

export const expectedAllDataTriageRequest: TriageRequest = {
  testDigestStatus: {
    alpha_test: {
      aaaaaaaaaaaaaaaaaaaaaaaaaaa: 'positive',
      bbbbbbbbbbbbbbbbbbbbbbbbbbb: 'negative',
      ddddddddddddddddddddddddddd: 'positive',
    },
    beta_test: {
      ccccccccccccccccccccccccccc: 'positive',
      ddddddddddddddddddddddddddd: 'negative',
    },
    gamma_test: {
      eeeeeeeeeeeeeeeeeeeeeeeeeee: '' as Label,
    }
  },
  changelist_id: 'someCL',
  crs: 'gerrit',
};
