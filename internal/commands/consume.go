package commands

import (
	"fmt"
)

func consumeHandler(c *Container, args []string) error {
	consumer, err := c.GetConsumer()
	if err != nil {
		return fmt.Errorf("failed to get consumer: %w", err)
	}

	c.logger.Info(nil, "Starting document consumption process...")
	if err := consumer.Consume(nil); err != nil {
		return fmt.Errorf("consumption failed: %w", err)
	}

	c.logger.Info(nil, "Document consumption process completed")
	return nil
}
