package testutil

import (
	"testing"

	"github.com/golang/mock/gomock"

	"github.com/babylonchain/covenant-emulator/testutil/mocks"
	"github.com/babylonchain/covenant-emulator/types"
)

func PrepareMockedClientController(t *testing.T, params *types.StakingParams) *mocks.MockClientController {
	ctl := gomock.NewController(t)
	mockClientController := mocks.NewMockClientController(ctl)

	mockClientController.EXPECT().Close().Return(nil).AnyTimes()
	mockClientController.EXPECT().QueryStakingParams().Return(params, nil).AnyTimes()

	return mockClientController
}
